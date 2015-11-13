package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/golang/glog"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var MAGIC_PMON_HEADER []byte = []byte("PMON")
var EXEC_SUCCESS []byte = []byte("PMON_SUCCESS\r\n")
var EXEC_FAIL []byte = []byte("PMON_FAIL\r\n")

func cp(dst, src string) error {
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
	if _, err := io.Copy(d, s); err != nil {
		d.Close()
		return err
	}
	return d.Close()
}

func recvFile(c io.ReadWriteCloser, path string) bool {
	headerbuf := make([]byte, 12)
	_, err := io.ReadFull(c, headerbuf)
	if err != nil {
		c.Close()
		return false
	}
	if !bytes.Equal(headerbuf[0:4], MAGIC_PMON_HEADER) {
		glog.Errorf("Invalid magic header")
		return false
	}
	var length uint64
	binary.Read(bytes.NewReader(headerbuf[4:12]), binary.LittleEndian, &length)

	var file *os.File
	file, err = os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0660)
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

var commandHandlers map[string]*commandHandler = make(map[string]*commandHandler)

func help(cmd []string, c io.ReadWriteCloser) bool {
	usage := `
Supported Commands:
PS                                 list current running process
System   <command> <args>          WARN:Exec System Command!
Rollback <File path>               Rollback updated file updated by 'fud'
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
	mproc := getService(cmd[0])
	if nil != mproc {
		startService(cmd[0], &LogTraceWriter{c})
	} else {
		io.WriteString(c, fmt.Sprintf("No process '%s' configured\r\n", cmd[0]))
		return false
	}
	return true
}
func stopProc(cmd []string, c io.ReadWriteCloser) bool {
	mproc := getService(cmd[0])
	if nil != mproc {
		killService(cmd[0], &LogTraceWriter{c})
	} else {
		io.WriteString(c, fmt.Sprintf("No process '%s' configured\r\n", cmd[0]))
		return false
	}
	return true
}
func restartProc(cmd []string, c io.ReadWriteCloser) bool {
	mproc := getService(cmd[0])
	if nil != mproc {
		killService(cmd[0], &LogTraceWriter{c})
		startService(cmd[0], &LogTraceWriter{c})
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
	} else {
		procCmd.Wait()
	}
	return true
}

func rollbackFile(args []string, c io.ReadWriteCloser) bool {
	path := strings.TrimSpace(args[0])
	backupPath := Cfg.BackupDir + "/" + path + ".bak"
	os.MkdirAll(filepath.Dir(backupPath), 0770)
	_, err := os.Stat(backupPath)
	if nil != err {
		io.WriteString(c, fmt.Sprintf("Failed rollback file:%s for reason:%v.", path, err))
		return false
	}
	mproc := getService(path)
	if nil != mproc {
		killService(path, &LogTraceWriter{c})
	}
	err = os.Rename(backupPath, path)
	if nil == err {
		io.WriteString(c, fmt.Sprintf("Rollback file:%s success.\r\n", path))
		if nil != mproc {
			startService(path, &LogTraceWriter{c})
		}
	} else {
		io.WriteString(c, fmt.Sprintf("Rollback file %s failed for reason:%v\r\n", path, err))
		if nil != mproc {
			mproc.autoRestart = true
		}
		return false
	}
	return true
}

func uploadFile(args []string, c io.ReadWriteCloser) bool {
	path := strings.TrimSpace(args[0])
	uploadPath := Cfg.UploadDir + "/" + path + ".new"
	backupPath := Cfg.BackupDir + "/" + path + ".bak"
	os.MkdirAll(filepath.Dir(uploadPath), 0770)
	os.MkdirAll(filepath.Dir(backupPath), 0770)
	if !recvFile(c, uploadPath) {
		return false
	}
	_, err := os.Stat(path)
	if nil == err || !os.IsNotExist(err) {
		err = cp(backupPath, path)
		if nil != err {
			io.WriteString(c, fmt.Sprintf("Failed backup file:%s for reason:%v.", path, err))
			return false
		}
	}
	mproc := getService(path)
	if nil != mproc {
		killService(path, &LogTraceWriter{c})
	} else {
		fmt.Printf("No proc found for %s\n", path)
	}
	err = os.Rename(uploadPath, path)
	if nil == err {
		io.WriteString(c, fmt.Sprintf("Update file:%s success.\r\n", path))
		if nil != mproc {
			os.Chmod(path, 0755)
			startService(path, &LogTraceWriter{c})
			mproc.autoRestart = true
		}
		return true
	} else {
		io.WriteString(c, fmt.Sprintf("Failed to rename update file %s for reason:%v\r\n", path, err))
		if nil != mproc {
			mproc.autoRestart = true
		}
		return false
	}
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
				rw.Write(EXEC_SUCCESS)
			} else {
				rw.Write(EXEC_FAIL)
			}
		} else {
			io.WriteString(rw, fmt.Sprintf("Error:unknown command:%v\r\n", cmd))
			continue
		}
	}
	c.Close()
}

func startAdminServer(laddr string) error {
	l, err := net.Listen("tcp", laddr)
	if nil != err {
		return err
	}
	for {
		c, _ := l.Accept()
		if nil != c {
			processAdminConn(c) //only ONE admin connection allowd
		}
	}
}

func init() {
	commandHandlers["help"] = &commandHandler{help, 0, 0}
	commandHandlers["ps"] = &commandHandler{ps, 0, 0}
	commandHandlers["system"] = &commandHandler{system, 0, -1}
	commandHandlers["exit"] = &commandHandler{quit, 0, 0}
	commandHandlers["quit"] = &commandHandler{quit, 0, 0}
	commandHandlers["upload"] = &commandHandler{uploadFile, 1, 1}
	commandHandlers["rollback"] = &commandHandler{rollbackFile, 1, 1}
	commandHandlers["start"] = &commandHandler{startProc, 1, 1}
	commandHandlers["restart"] = &commandHandler{restartProc, 1, 1}
	commandHandlers["stop"] = &commandHandler{stopProc, 1, 1}
	commandHandlers["shutdown"] = &commandHandler{shutdown, 0, 0}
}
