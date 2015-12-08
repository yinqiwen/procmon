package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/golang/glog"
)

var MagicPmonHeader = []byte("PMON")
var ExecSuccess = []byte("PMON_SUCCESS\r\n")
var ExecFail = []byte("PMON_FAIL\r\n")

func cp(dst, src string, mod os.FileMode) error {
	s, err := os.Open(src)
	if err != nil {
		return err
	}
	// no need to check errors on read only file, we already got everything
	// we need from the filesystem, so nothing can go wrong now.
	defer s.Close()
	d, err := os.Create(dst)
	if err != nil {
		return err
	}
	d.Chmod(mod)
	if _, err := io.Copy(d, s); err != nil {
		d.Close()
		return err
	}
	return d.Close()
}

func cleanOldBackupFiles(path string) {
	basename := filepath.Base(path)
	dir := filepath.Dir(path)
	files, _ := ioutil.ReadDir(dir)
	var filePaths []string
	for _, f := range files {
		fpath := dir + "/" + f.Name()
		if strings.HasPrefix(f.Name(), basename) {
			filePaths = append(filePaths, fpath)
		}
	}
	if len(filePaths) > Cfg.MaxBackupFile {
		sort.Strings(filePaths)
		for i := 0; i < len(filePaths)-Cfg.MaxBackupFile; i++ {
			os.Remove(filePaths[i])
		}
	}
}

func recvFile(c io.ReadWriteCloser, path string, mod os.FileMode) bool {
	headerbuf := make([]byte, 12)
	_, err := io.ReadFull(c, headerbuf)
	if err != nil {
		c.Close()
		return false
	}
	if !bytes.Equal(headerbuf[0:4], MagicPmonHeader) {
		glog.Errorf("Invalid magic header")
		return false
	}
	var length uint64
	binary.Read(bytes.NewReader(headerbuf[4:12]), binary.LittleEndian, &length)

	var file *os.File
	file, err = os.OpenFile(path, os.O_CREATE|os.O_RDWR, mod)
	if nil != err {
		io.WriteString(c, fmt.Sprintf("Open file:%s failed for reason:%v\r\n", path, err))
		c.Close()
		return false
	}
	var n int64
	n, err = io.CopyN(file, c, int64(length))
	if n != int64(length) || nil != err {
		c.Close()
		file.Close()
		os.Remove(path)
		return false
	}
	file.Close()
	return true
}

type LogTraceWriter struct {
	wr io.Writer
}

func (c *LogTraceWriter) Write(p []byte) (int, error) {
	c.wr.Write(p)
	glog.Infof("%s", string(p))
	return len(p), nil
}

type commandHandler struct {
	handler func([]string, io.ReadWriteCloser) bool
	minArgs int
	maxArgs int
}

var commandHandlers = make(map[string]*commandHandler)

func help(cmd []string, c io.ReadWriteCloser) bool {
	usage := `
Supported Commands:
PS                                 list current running process
System   <command> <args>          WARN:Exec System Command!
Rollback <File path> <Postfix>     Rollback updated file
Start   <Process>                  Start process
Restart <Process>                  WARN:restart process
Stop    <Process>                  WARN:stop process
Shutdown                           WARN:Stop whole service
Exit                               exit current connection
`
	c.Write([]byte(usage))
	return true
}

func ps(cmd []string, c io.ReadWriteCloser) bool {
	listProcs(c)
	return true
}

func quit(cmd []string, c io.ReadWriteCloser) bool {
	c.Close()
	return true
}

func startProc(cmd []string, c io.ReadWriteCloser) bool {
	procs := getProcListByName(cmd[0])
	if len(procs) > 0 {
		tracer := &LogTraceWriter{c}
		for _, proc := range procs {
			if nil != proc {
				proc.start(tracer)
			}
		}
	} else {
		io.WriteString(c, fmt.Sprintf("No process '%s' configured\r\n", cmd[0]))
		return false
	}
	return true
}
func stopProc(cmd []string, c io.ReadWriteCloser) bool {
	procs := getProcListByName(cmd[0])
	if len(procs) > 0 {
		tracer := &LogTraceWriter{c}
		for _, proc := range procs {
			if nil != proc {
				proc.kill(tracer)
			}
		}
	} else {
		io.WriteString(c, fmt.Sprintf("No process '%s' configured\r\n", cmd[0]))
		return false
	}
	return true
}
func restartProc(cmd []string, c io.ReadWriteCloser) bool {
	procs := getProcListByName(cmd[0])
	if len(procs) > 0 {
		tracer := &LogTraceWriter{c}
		for _, proc := range procs {
			if nil != proc {
				proc.kill(tracer)
				proc.start(tracer)
			}
		}
	} else {
		io.WriteString(c, fmt.Sprintf("No process '%s' configured\r\n", cmd[0]))
		return false
	}
	return true
}

func system(cmd []string, c io.ReadWriteCloser) bool {
	var procCmd *exec.Cmd
	if len(cmd) > 1 {
		procCmd = exec.Command(cmd[0], cmd[1:]...)
	} else {
		procCmd = exec.Command(cmd[0])
	}
	procCmd.Env = os.Environ()
	procCmd.Stdout = c
	procCmd.Stderr = c
	err := procCmd.Run()
	if nil != err {
		io.WriteString(c, fmt.Sprintf("Failed to exec command for reason:%v\r\n", err))
		return false
	}
	procCmd.Wait()
	return true
}

func rollbackFile(args []string, c io.ReadWriteCloser) bool {
	path := strings.TrimSpace(args[0])
	basename := filepath.Base(path)
	backupDir := filepath.Dir(Cfg.BackupDir + "/" + path)
	backupFiles, _ := ioutil.ReadDir(backupDir)

	var backupPath string
	if len(args) == 1 {
		var backupPaths []string
		for _, f := range backupFiles {
			//only select files with name format <name>.<timestamp>, the timestamp have 14 number chars
			if matched, _ := regexp.MatchString(basename+".[0-9]{14}", f.Name()); matched {
				fpath := backupDir + "/" + f.Name()
				backupPaths = append(backupPaths, fpath)
			}
		}
		sort.Strings(backupPaths)
		if len(backupPaths) == 0 {
			fmt.Fprintf(c, "Failed rollback file:%s bacauseof no backup files found.", path)
			return false
		}
		backupPath = backupPaths[len(backupPaths)-1]
	} else {
		backupPath = backupDir + "/" + basename + "." + args[1]
	}
	st, err := os.Stat(backupPath)
	if nil != err {
		io.WriteString(c, fmt.Sprintf("Failed rollback file:%s for reason:%v.", path, err))
		return false
	}
	procs := getProcListByName(path)
	tracer := &LogTraceWriter{c}
	for _, proc := range procs {
		if nil != proc {
			proc.kill(tracer)
		}
	}
	err = cp(path, backupPath, st.Mode())
	if nil == err {
		io.WriteString(c, fmt.Sprintf("Rollback file:%s from %s success.\r\n", path, backupPath))
		for _, proc := range procs {
			if nil != proc {
				proc.start(tracer)
			}
		}
		if path == os.Args[0] {
			restartSelf(c)
		}
	} else {
		io.WriteString(c, fmt.Sprintf("Rollback file %s failed for reason:%v\r\n", path, err))
	}
	for _, proc := range procs {
		if nil != proc {
			proc.autoRestart = true
		}
	}
	return nil == err
}

func uploadFile(args []string, c io.ReadWriteCloser) bool {
	path := strings.TrimSpace(args[0])
	uploadPath := Cfg.UploadDir + "/" + path + ".new"
	os.MkdirAll(filepath.Dir(uploadPath), 0770)
	defaultPerm := os.FileMode(0660)
	if st, err := os.Lstat(path); nil == err {
		defaultPerm = st.Mode()
	}
	if !recvFile(c, uploadPath, defaultPerm) {
		return false
	}
	st, err := os.Stat(path)
	if nil == err {
		backupPath := Cfg.BackupDir + "/" + path + st.ModTime().Format(".20060102150405")
		os.MkdirAll(filepath.Dir(backupPath), 0770)
		err = cp(backupPath, path, defaultPerm)
		if nil != err {
			io.WriteString(c, fmt.Sprintf("Failed backup file:%s for reason:%v\n", path, err))
			return false
		}
		fmt.Fprintf(c, "Backup file %s to %s succeess.\r\n", path, backupPath)
		cleanOldBackupFiles(backupPath)
	}
	procs := getProcListByName(path)
	tracer := &LogTraceWriter{c}
	for _, proc := range procs {
		if nil != proc {
			proc.kill(tracer)
		}
	}

	err = os.Rename(uploadPath, path)
	if nil == err {
		io.WriteString(c, fmt.Sprintf("Update file:%s success.\r\n", path))
		if len(procs) > 0 {
			os.Chmod(path, 0755)
			for _, proc := range procs {
				if nil != proc {
					proc.start(tracer)
				}
			}
		}
		if path == os.Args[0] {
			restartSelf(c)
		}
	} else {
		io.WriteString(c, fmt.Sprintf("Failed to rename update file %s for reason:%v\r\n", path, err))
	}
	for _, proc := range procs {
		if nil != proc {
			proc.autoRestart = true
		}
	}
	return nil == err
}

func shutdown(cmd []string, c io.ReadWriteCloser) bool {
	killAll(&LogTraceWriter{c})
	return true
}

type bufReaderDirectWriter struct {
	*bufio.Reader
	io.Writer
	io.Closer
}

func processAdminConn(c net.Conn) {
	authed := false
	if len(Cfg.Auth) == 0 {
		authed = true
	}
	rw := &bufReaderDirectWriter{bufio.NewReader(c), c, c}

	for {
		line, _, err := rw.ReadLine()
		if nil != err {
			break
		}
		if !authed {
			if Cfg.Auth == strings.TrimSpace(string(line)) {
				authed = true
				continue
			} else {
				io.WriteString(rw, fmt.Sprintf("Connection auth failed\r\n"))
				rw.Close()
				return
			}
		}
		if len(line) > 1024 {
			glog.Errorf("Too long command from client.")
			rw.Close()
			break
		}
		cmd := strings.Fields(string(line))
		if h, ok := commandHandlers[strings.ToLower(cmd[0])]; ok {
			args := cmd[1:]
			if (h.minArgs >= 0 && len(args) < h.minArgs) || (h.maxArgs >= 0 && len(args) > h.maxArgs) {
				io.WriteString(rw, fmt.Sprintf("Invalid command args:%v for '%s'\r\n", args, cmd[0]))
				continue
			}
			glog.Infof("Execute %v from client:%v", cmd, c.RemoteAddr())
			if h.handler(args, rw) {
				rw.Write(ExecSuccess)
			} else {
				rw.Write(ExecFail)
			}
		} else {
			io.WriteString(rw, fmt.Sprintf("Error:unknown command:%v\r\n", cmd))
			continue
		}
	}
	c.Close()
}

func init() {
	commandHandlers["help"] = &commandHandler{help, 0, 0}
	commandHandlers["ps"] = &commandHandler{ps, 0, 0}
	commandHandlers["system"] = &commandHandler{system, 0, -1}
	commandHandlers["exit"] = &commandHandler{quit, 0, 0}
	commandHandlers["quit"] = &commandHandler{quit, 0, 0}
	commandHandlers["upload"] = &commandHandler{uploadFile, 1, 1}
	commandHandlers["rollback"] = &commandHandler{rollbackFile, 1, 2}
	commandHandlers["start"] = &commandHandler{startProc, 1, 1}
	commandHandlers["restart"] = &commandHandler{restartProc, 1, 1}
	commandHandlers["stop"] = &commandHandler{stopProc, 1, 1}
	commandHandlers["shutdown"] = &commandHandler{shutdown, 0, 0}
}
