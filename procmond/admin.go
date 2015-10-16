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
	"strings"
)

var MAGIC_PMON_HEADER []byte = []byte("PMON")

func help(c net.Conn) {
	usage := `
Supported Commands:
PS                                 list current running process
System   <command> <args>          WARN:Exec System Command!
Roolback <File path>               Rollback updated file updated by 'fud'
Start   <Process>                  Start process
Restart <Process>                  WARN:restart process
Stop    <Process>                  WARN:stop process
Exit                               exit current connection
`
	c.Write([]byte(usage))
}

func ps(c net.Conn) {
	listProcs(c)
}

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

func recvFile(bc *bufio.Reader, c net.Conn, path string) bool {
	headerbuf := make([]byte, 12)
	_, err := io.ReadFull(bc, headerbuf)
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
	n, err = io.CopyN(file, bc, int64(length))
	if n != int64(length) || nil != err {
		c.Close()
		file.Close()
		os.Remove(path)
		return false
	}
	file.Close()
	return true
}

type ConnWriter struct {
	conn net.Conn
}

func (c *ConnWriter) Write(p []byte) (int, error) {
	c.conn.Write(p)
	return len(p), nil
}

func system(cmd []string, c net.Conn) {
	procCmd := exec.Command(cmd[0], cmd[1:]...)
	procCmd.Env = os.Environ()
	procCmd.Stdout = &ConnWriter{c}
	procCmd.Stderr = &ConnWriter{c}
	err := procCmd.Run()
	if nil != err {
		//fmt.Printf("exec %v failed\n", cmd)
		io.WriteString(c, fmt.Sprintf("Failed to exec command for reason:%v\r\n", err))
	} else {
		//io.WriteString(c, fmt.Sprintf("exec %v success\r\n", cmd))
		procCmd.Wait()
	}
}

func rollbackFile(c net.Conn, path string) {
	path = strings.TrimSpace(path)
	_, err := os.Stat(path + ".bak")
	if nil != err {
		io.WriteString(c, fmt.Sprintf("Failed rollback file:%s for reason:%v.", path, err))
		return
	}
	mproc := getService(path)
	if nil != mproc {
		killService(path, c)
	}
	err = os.Rename(path+".bak", path)
	if nil == err {
		io.WriteString(c, fmt.Sprintf("Rollback file:%s success.\r\n", path))
		if nil != mproc {
			startService(path, c)
			mproc.autoRestartSrv = true
		}
	} else {
		io.WriteString(c, fmt.Sprintf("Rollback file %s failed for reason:%v\r\n", path, err))
		if nil != mproc {
			mproc.autoRestartSrv = true
		}
	}
}

func updateFile(bc *bufio.Reader, c net.Conn, path string) bool {
	path = strings.TrimSpace(path)
	if !recvFile(bc, c, path+".new") {
		return false
	}
	_, err := os.Stat(path)
	if nil == err || !os.IsNotExist(err) {
		err = cp(path+".bak", path)
		if nil != err {
			io.WriteString(c, fmt.Sprintf("Failed backup file:%s for reason:%v.", path, err))
			return false
		}
	}
	mproc := getService(path)
	if nil != mproc {
		killService(path, c)
	} else {
		fmt.Printf("No proc found for %s\n", path)
	}
	err = os.Rename(path+".new", path)
	if nil == err {
		io.WriteString(c, fmt.Sprintf("Update file:%s success.\r\n", path))
		if nil != mproc {
			startService(path, c)
			mproc.autoRestartSrv = true
		}
		return true
	} else {
		io.WriteString(c, fmt.Sprintf("Failed to rename update file %s for reason:%v\r\n", path, err))
		if nil != mproc {
			mproc.autoRestartSrv = true
		}
		return false
	}
}

func processAdminConn(c net.Conn) {
	bc := bufio.NewReader(c)
	for {
		line, _, err := bc.ReadLine()
		if nil != err {
			break
		}
		cmd := strings.Fields(string(line))
		if strings.EqualFold(cmd[0], "help") {
			help(c)
		} else if strings.EqualFold(cmd[0], "exit") || strings.EqualFold(cmd[0], "quit") {
			break
		} else if strings.EqualFold(cmd[0], "restart") {
			if len(cmd) != 2 {
				c.Write([]byte("Invalid command\r\n"))
				help(c)
			} else {
				restartService(cmd[1], c)
			}
		} else if strings.EqualFold(cmd[0], "start") {
			if len(cmd) != 2 {
				c.Write([]byte("Invalid command\r\n"))
				help(c)
			} else {
				startService(cmd[1], c)
			}

		} else if strings.EqualFold(cmd[0], "stop") {
			if len(cmd) != 2 {
				c.Write([]byte("Invalid command\r\n"))
				help(c)
			} else {
				killService(cmd[1], c)
			}
		} else if strings.EqualFold(cmd[0], "ps") {
			ps(c)
		} else if strings.EqualFold(cmd[0], "update") {
			if len(cmd) != 2 {
				c.Write([]byte("Invalid command\r\n"))
				c.Write([]byte("\nfail\r\n"))
			} else {
				succ := updateFile(bc, c, cmd[1])
				if succ {
					c.Write([]byte("\nsuccess\r\n"))
				} else {
					c.Write([]byte("\nfail\r\n"))
				}
			}
		} else if strings.EqualFold(cmd[0], "system") {
			system(cmd[1:], c)
		} else if strings.EqualFold(cmd[0], "rollback") {
			if len(cmd) != 2 {
				c.Write([]byte("Invalid command\r\n"))
				help(c)
			} else {
				rollbackFile(c, cmd[1])
			}
		} else {
			io.WriteString(c, fmt.Sprintf("Error:unknown command:%v\r\n", cmd))
			help(c)
		}
		c.Write([]byte("\r\n"))
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
