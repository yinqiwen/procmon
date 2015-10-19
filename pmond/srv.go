package main

import (
	"fmt"
	"github.com/golang/glog"
	"io"
	//"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type monitorProc struct {
	processName      string
	args             []string
	procCmd          *exec.Cmd
	autoRestart      bool
	restartCondFiles []string
}

func (mp *monitorProc) shouldRestart(updateFile string) bool {
	for _, f := range mp.restartCondFiles {
		if f == updateFile {
			return true
		}
	}
	return false
}

type monitorProcTable struct {
	procNames    []string
	monitorProcs map[string]*monitorProc
	mlk          sync.Mutex
}

var procTable *monitorProcTable

func newMonitorProcTable() *monitorProcTable {
	mp := new(monitorProcTable)
	mp.monitorProcs = make(map[string]*monitorProc)
	return mp
}

func buildMonitorProcs() {
	procTable.mlk.Lock()
	procTable.procNames = make([]string, 0)
	for _, proc := range Cfg.Monitor {
		cmd := strings.Fields(proc.Proc)
		mproc, ok := procTable.monitorProcs[cmd[0]]
		procTable.procNames = append(procTable.procNames, cmd[0])
		if !ok {
			mproc := new(monitorProc)
			mproc.processName = cmd[0]
			mproc.autoRestart = true
			procTable.monitorProcs[mproc.processName] = mproc
		} else {
			mproc.args = cmd[1:]
		}
	}
	procTable.mlk.Unlock()
}

func getService(proc string) *monitorProc {
	procTable.mlk.Lock()
	defer procTable.mlk.Unlock()
	if mproc, ok := procTable.monitorProcs[proc]; ok {
		return mproc
	} else {
		return nil
	}
}

func listProcs(wr io.Writer) {
	procTable.mlk.Lock()
	defer procTable.mlk.Unlock()
	wr.Write([]byte("PID   Process	Status\r\n"))
	for proc, mproc := range procTable.monitorProcs {
		pid := -1
		status := "stoped"
		if nil != mproc.procCmd {
			pid = mproc.procCmd.Process.Pid
			status = "running"
		}
		io.WriteString(wr, fmt.Sprintf("%d   %s	%s\r\n", pid, proc, status))
	}
}

func killService(proc string, wr io.Writer) {
	mproc := getService(proc)
	if nil != mproc && nil != mproc.procCmd {
		mproc.procCmd.Process.Kill()
		mproc.autoRestart = false
		for {
			if nil == mproc.procCmd {
				io.WriteString(wr, fmt.Sprintf("Kill process:%s success.\r\n", proc))
				break
			} else {
				io.WriteString(wr, fmt.Sprintf("Process:%s not killed, wait 1 sec.\r\n", proc))
				time.Sleep(time.Second)
			}
		}
	} else {
		io.WriteString(wr, fmt.Sprintf("No running process:%s\r\n", proc))
	}
}

func restartService(proc string, wr io.Writer) {
	mproc := getService(proc)
	if nil != mproc {
		killService(proc, wr)
	}
	startService(proc, wr)
}

func waitService(mproc *monitorProc) {
	if nil == mproc || nil == mproc.procCmd {
		return
	}
	mproc.procCmd.Wait()
	mproc.procCmd.Process.Release()
	mproc.procCmd = nil
	glog.Infof("Process:%s stoped.", mproc.processName)
}

func startService(proc string, wr io.Writer) {
	mproc := getService(proc)
	if nil != mproc && nil != mproc.procCmd {
		io.WriteString(wr, fmt.Sprintf("Process:%s already started.\r\n", proc))
		return
	}
	var err error
	mproc.procCmd = exec.Command(mproc.processName, mproc.args...)
	err = mproc.procCmd.Start()
	if err != nil {
		mproc.procCmd = nil
		io.WriteString(wr, fmt.Sprintf("Failed to start process:%s for reason:%v\r\n", proc, err))
		return
	}
	io.WriteString(wr, fmt.Sprintf("Start process:%s success.\r\n", proc))
	mproc.autoRestart = true
	go waitService(mproc)
}

func init() {
	procTable = newMonitorProcTable()
	routine := func() {
		checkTickChan := time.NewTicker(time.Millisecond * 1000).C
		for {
			select {
			case <-checkTickChan:
				for _, proc := range procTable.procNames {
					mproc := getService(proc)
					if nil != mproc && mproc.procCmd == nil && mproc.autoRestart {
						startService(proc, &LogWriter{})
					}
				}
			}
		}
	}
	go routine()
}
