package main

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/golang/glog"
	"github.com/yinqiwen/gotoolkit/iotools"
)

var listenFile *os.File

type ProcOutput struct {
	fname        string
	crashOutput  bool
	crashContent bytes.Buffer
	proc         *monitorProc
	log          *iotools.RotateFile
}

func (pout *ProcOutput) reopen() {
	if nil != pout.log {
		pout.log.Close()
	}
	rfile := &iotools.RotateFile{
		MaxBackupIndex:  2,
		MaxFileSize:     1024 * 1024 * 1024,
		SyncBytesPeriod: 64 * 1024,
	}
	err := rfile.Open(pout.fname, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0660)
	if nil != err {
		glog.Errorf("%v", err)
		return
	}
	pout.log = rfile
}

func (pout *ProcOutput) Write(p []byte) (int, error) {
	if nil == pout.log {
		pout.reopen()
	}
	if nil == pout.log {
		return 0, fmt.Errorf("Log file %s not open", pout.fname)
	}
	if pout.crashOutput || (len(pout.proc.cfg.Crash.Prefix) > 0 && bytes.HasPrefix(p, []byte(pout.proc.cfg.Crash.Prefix))) {
		pout.crashOutput = true
		pout.crashContent.Write(p)
	}
	return pout.log.Write(p)
}

func (pout *ProcOutput) Close() error {
	if nil == pout.log {
		return nil
	}
	err := pout.log.Close()
	if nil == err {
		pout.log = nil
	}
	return err
}

type monitorProc struct {
	processName   string
	args          []string
	procCmd       *exec.Cmd
	output        *ProcOutput
	autoRestart   bool
	cfg           procConfig
	lastCheckTime int64
	lk            sync.Mutex
}

func (mproc *monitorProc) isRunning() bool {
	mproc.lk.Lock()
	defer mproc.lk.Unlock()
	return mproc.procCmd != nil
}

func (mproc *monitorProc) wait() bool {
	if !mproc.isRunning() {
		return false
	}
	mproc.lk.Lock()
	cmd := mproc.procCmd
	output := mproc.output
	crashFileName := fmt.Sprintf("%s/%s-crash-%d.log", Cfg.LogDir, filepath.Base(mproc.processName), cmd.Process.Pid)
	mproc.lk.Unlock()
	cmd.Wait()
	glog.Infof("Process:%s %v stoped.", mproc.processName, mproc.args)
	mproc.lk.Lock()
	defer mproc.lk.Unlock()
	if cmd == mproc.procCmd {
		mproc.procCmd = nil
	}

	if output.crashContent.Len() > 0 {
		ioutil.WriteFile(crashFileName, output.crashContent.Bytes(), 0666)
		if len(mproc.cfg.Crash.Command) > 0 {
			args := mproc.cfg.Crash.Command[1:]
			for i := 0; i < len(args); i++ {
				args[i] = strings.Replace(args[i], "${CrashContent}", output.crashContent.String(), -1)
				args[i] = strings.Replace(args[i], "${HOSTNAME}", os.Getenv("HOSTNAME"), -1)
			}
			exec.Command(mproc.cfg.Crash.Command[0], args...).Run()
		}
	}
	return true
}

func (mproc *monitorProc) kill(wr io.Writer) {
	if mproc.isRunning() {
		mproc.lk.Lock()
		mproc.autoRestart = false
		mproc.procCmd.Process.Kill()
		mproc.lk.Unlock()
		for {
			if !mproc.isRunning() {
				io.WriteString(wr, fmt.Sprintf("Kill process:%s success.\r\n", mproc.processName))
				break
			} else {
				io.WriteString(wr, fmt.Sprintf("Process:%s not killed, wait 1 sec.\r\n", mproc.processName))
				time.Sleep(time.Second)
			}
		}
	} else {
		io.WriteString(wr, fmt.Sprintf("No running process:%s\r\n", mproc.processName))
	}
}

func (mproc *monitorProc) check(wr io.Writer) bool {
	if mproc.autoRestart && !mproc.isRunning() {
		mproc.start(&LogWriter{})
		return true
	}
	if !mproc.isRunning() && !mproc.autoRestart {
		return false
	}
	if len(mproc.cfg.Check.Addr) == 0 {
		return false
	}
	now := time.Now().Unix()
	if mproc.lastCheckTime == 0 {
		mproc.lastCheckTime = now
	}
	if now-mproc.lastCheckTime >= int64(mproc.cfg.Check.Period) {
		mproc.lastCheckTime = now
		c, err := net.DialTimeout("tcp", mproc.cfg.Check.Addr, time.Duration(mproc.cfg.Check.Timeout)*time.Second)
		if nil != err {
			mproc.procCmd.Process.Kill()
			glog.Errorf("Kill process:%s since check failed by reason:%v", mproc.processName, err)
			return true
		}
		c.Close()
	}
	return false
}

func (mproc *monitorProc) start(wr io.Writer) {
	if mproc.isRunning() {
		io.WriteString(wr, fmt.Sprintf("Process:%s already started.\r\n", mproc.processName))
		return
	}
	var err error
	mproc.lk.Lock()
	defer mproc.lk.Unlock()
	mproc.procCmd = exec.Command(mproc.processName, mproc.args...)
	mproc.procCmd.Env = append(os.Environ(), mproc.cfg.Env...)

	var stderrpipe, stdoutpipe io.ReadCloser
	stderrpipe, err = mproc.procCmd.StderrPipe()
	if nil == err {
		stdoutpipe, err = mproc.procCmd.StdoutPipe()
	}
	if nil != err {
		glog.Errorf("%v", err)
	} else {
		if nil != mproc.output {
			mproc.output.Close()
		}
		mproc.output = &ProcOutput{}
		mproc.output.fname = mproc.cfg.LogFile
		mproc.output.proc = mproc
		go func() {
			io.Copy(mproc.output, stderrpipe)
		}()
		go func() {
			io.Copy(mproc.output, stdoutpipe)
		}()
	}
	err = mproc.procCmd.Start()
	if err != nil {
		mproc.procCmd = nil
		io.WriteString(wr, fmt.Sprintf("Failed to start process:%s for reason:%v\r\n", mproc.processName, err))
		return
	}

	io.WriteString(wr, fmt.Sprintf("Start process:%s %v success.\r\n", mproc.processName, mproc.args))
	mproc.autoRestart = true
	go mproc.wait()
}

type monitorProcTable struct {
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
	if Cfg.MaxBackupFile == 0 {
		Cfg.MaxBackupFile = 10
	}
	for _, proc := range Cfg.Monitor {
		cmd := strings.Fields(proc.Proc)
		mproc, ok := procTable.monitorProcs[proc.Proc]
		//procTable.procNames = append(procTable.procNames, cmd[0])
		if !ok {
			mproc = new(monitorProc)
			mproc.processName = cmd[0]
			mproc.args = cmd[1:]
			mproc.autoRestart = true
			procTable.monitorProcs[proc.Proc] = mproc
		}
		mproc.cfg = proc
		if len(mproc.cfg.LogFile) == 0 {
			mproc.cfg.LogFile = filepath.Base(mproc.processName) + ".out"
		}
		if !strings.HasPrefix(mproc.cfg.LogFile, "/") {
			mproc.cfg.LogFile = Cfg.LogDir + "/" + mproc.cfg.LogFile
		}
	}
	procTable.mlk.Unlock()
}

func getService(proc string) *monitorProc {
	procTable.mlk.Lock()
	defer procTable.mlk.Unlock()
	if mproc, ok := procTable.monitorProcs[proc]; ok {
		return mproc
	}
	return nil
}

func getProcListByName(name string) []*monitorProc {
	var procs []*monitorProc
	procTable.mlk.Lock()
	defer procTable.mlk.Unlock()
	for k, proc := range procTable.monitorProcs {
		if strings.HasPrefix(k, name) {
			procs = append(procs, proc)
		}
	}
	return procs
}

func listProcs(wr io.Writer) {
	procTable.mlk.Lock()
	defer procTable.mlk.Unlock()
	wr.Write([]byte("PID   Process	Args		Status\r\n"))
	for _, mproc := range procTable.monitorProcs {
		pid := -1
		status := "stoped"
		if mproc.isRunning() {
			pid = mproc.procCmd.Process.Pid
			status = "running"
		}
		io.WriteString(wr, fmt.Sprintf("%d   %s	%v		%s\r\n", pid, mproc.processName, mproc.args, status))
	}
}

var pidFile string = ".pids"

func killAll(wr io.Writer) {
	for _, mproc := range procTable.monitorProcs {
		if nil != mproc {
			mproc.kill(wr)
		}
	}
	os.Exit(1)
}

func dumpPids() {
	file, err := os.Create(pidFile)
	if nil != err {
		glog.Error(err)
		return
	}
	defer file.Close()
	fmt.Fprintf(file, "%d\n", os.Getpid())
	procTable.mlk.Lock()
	defer procTable.mlk.Unlock()
	for _, mproc := range procTable.monitorProcs {
		if mproc.isRunning() {
			fmt.Fprintf(file, "%d\n", mproc.procCmd.Process.Pid)
		}
	}
}

func restartSelf(wr io.Writer) {
	for _, mproc := range procTable.monitorProcs {
		if nil != mproc {
			mproc.kill(wr)
		}
	}
	path := os.Args[0]
	args := os.Args[1:]
	hasGracefulFlal := false
	for _, arg := range args {
		if arg == "-graceful" {
			hasGracefulFlal = true
			break
		}
	}
	if !hasGracefulFlal {
		args = append(args, "-graceful")
	}
	fmt.Fprintf(wr, "Restart pmond self.\n")
	cmd := exec.Command(path, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = []*os.File{listenFile}

	err := cmd.Start()
	if err != nil {
		glog.Fatalf("gracefulRestart: Failed to launch, error: %v", err)
	}
}

func init() {
	procTable = newMonitorProcTable()
	routine := func() {
		dumpPids()
		checkTickChan := time.NewTicker(time.Millisecond * 1000).C
		for {
			select {
			case <-checkTickChan:
				changed := false
				for _, procCfg := range Cfg.Monitor {
					mproc := getService(procCfg.Proc)
					if nil != mproc {
						if mproc.check(&LogWriter{}) {
							changed = true
						}
					}
				}
				if changed {
					dumpPids()
				}
			}
		}
	}
	go routine()
}
