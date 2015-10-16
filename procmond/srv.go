package main

import (
	"fmt"
	"io"
	//"io/ioutil"
	//"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type monitorProc struct {
	processName    string
	args           []string
	procCmd        *exec.Cmd
	autoRestartSrv bool
}

var monitorProcs map[string]*monitorProc = make(map[string]*monitorProc)
var mlk sync.Mutex

func buildMonitorProcs() {
	procMaps := make(map[string]*monitorProc)
	for _, proc := range Cfg.Monitor {
		cmd := strings.Fields(proc.Proc)
		mproc := new(monitorProc)
		mproc.processName = cmd[0]
		mproc.args = cmd[1:]
		mproc.autoRestartSrv = true
		procMaps[mproc.processName] = mproc
	}
	mlk.Lock()
	monitorProcs = procMaps
	mlk.Unlock()
}

func getService(proc string) *monitorProc {
	mlk.Lock()
	defer mlk.Unlock()
	if mproc, ok := monitorProcs[proc]; ok {
		return mproc
	} else {
		return nil
	}
}

func listProcs(wr io.Writer) {
	mlk.Lock()
	defer mlk.Unlock()
	wr.Write([]byte("PID   Process	Status\r\n"))
	for proc, mproc := range monitorProcs {
		pid := -1
		status := "stoped"
		if nil != mproc.procCmd {
			pid = mproc.procCmd.Process.Pid
			status = "running"
		}
		io.WriteString(wr, fmt.Sprintf("%d   %s	%s\r\n", pid, proc, status))
	}
}

func getProcNames() []string {
	mlk.Lock()
	ret := make([]string, 0)
	for k, _ := range monitorProcs {
		ret = append(ret, k)
	}
	mlk.Unlock()
	return ret
}

func killService(proc string, wr io.Writer) {
	mproc := getService(proc)
	if nil != mproc && nil != mproc.procCmd {
		mproc.procCmd.Process.Kill()
		mproc.autoRestartSrv = false
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
	mproc.autoRestartSrv = true
	go waitService(mproc)
}

func init() {
	routine := func() {
		checkTickChan := time.NewTicker(time.Millisecond * 1000).C
		for {
			select {
			case <-checkTickChan:
				procs := getProcNames()
				for _, proc := range procs {
					mproc := getService(proc)
					if nil != mproc && mproc.procCmd == nil && mproc.autoRestartSrv {
						startService(proc, &LogWriter{})
					}
				}
			}
		}
	}
	go routine()
}
