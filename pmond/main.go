package main

import (
	"encoding/json"
	"flag"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/golang/glog"
)

type checkConfig struct {
	Addr    string
	Period  int
	Timeout int
}

type procConfig struct {
	Proc    string
	LogFile string
	Env     []string
	Check   checkConfig
}

type procMonConfig struct {
	Listen        string
	Auth          string
	BackupDir     string
	MaxBackupFile int
	UploadDir     string
	LogDir        string
	Monitor       []procConfig
}

var Cfg procMonConfig
var confPath string

type LogWriter struct {
}

func (lw *LogWriter) Write(p []byte) (int, error) {
	glog.Infof("%s", string(p))
	return len(p), nil
}

func watchConfFile() {
	confFileTime := int64(0)
	reload := func() {
		file, err := os.Open(confPath)
		if nil != err {
			glog.Errorf("%v\n", err)
			return
		}
		defer file.Close()
		st, err := file.Stat()
		if nil != err {
			glog.Errorf("%v\n", err)
			return
		}
		if st.ModTime().Unix() > confFileTime {
			data, _ := ioutil.ReadAll(file)
			err = json.Unmarshal(data, &Cfg)
			if nil != err {
				glog.Errorf("Failed to unmarshal json to config for reason:%v", err)
				return
			}
			if len(Cfg.UploadDir) == 0 {
				Cfg.UploadDir = "./upload"
			}
			if len(Cfg.BackupDir) == 0 {
				Cfg.BackupDir = "./backup"
			}

			if len(Cfg.LogDir) == 0 {
				if logDirFlag := flag.Lookup("log_dir"); nil != logDirFlag {
					Cfg.LogDir = logDirFlag.Value.String()
				} else {
					Cfg.LogDir = "./logs"
				}
			}
			os.MkdirAll(Cfg.UploadDir, 0770)
			os.MkdirAll(Cfg.BackupDir, 0770)
			os.MkdirAll(Cfg.LogDir, 0770)
			confFileTime = st.ModTime().Unix()
			buildMonitorProcs()
		}
	}

	reload()
	go func() {
		for {
			time.Sleep(5 * time.Second)
			reload()
		}
	}()
}

func main() {
	conf := flag.String("conf", "./conf/pmon.json", "config file")
	var gracefulChild bool
	flag.BoolVar(&gracefulChild, "graceful", false, "listen on fd open 3 (internal use only)")
	flag.Parse()
	defer glog.Flush()

	var err error
	confPath, err = filepath.Abs(*conf)
	if nil != err {
		glog.Errorf("%v", err)
		return
	}

	// sc := make(chan os.Signal, 1)
	// signal.Notify(sc, os.Interrupt, os.Kill)
	// go func() {
	// 	_ = <-sc
	// 	killAll(&LogWriter{})
	// 	glog.Flush()
	// 	os.Exit(1)
	// }()

	watchConfFile()

	//start admin server
	var l net.Listener

	if gracefulChild {
		f := os.NewFile(3, "")
		l, err = net.FileListener(f)
	} else {
		l, err = net.Listen("tcp", Cfg.Listen)
	}
	if nil != err {
		glog.Errorf("Bind socket failed:%v", err)
		return
	}
	tl := l.(*net.TCPListener)
	listenFile, _ = tl.File()
	if gracefulChild {
		parent := syscall.Getppid()
		glog.Infof("main: Killing parent pid: %v", parent)
		if proc, _ := os.FindProcess(parent); nil != proc {
			proc.Signal(syscall.SIGTERM)
		}
		//syscall.Kill(parent, syscall.SIGTERM)
	}
	for {
		c, _ := l.Accept()
		if nil != c {
			processAdminConn(c) //only ONE admin connection allowd
		}
	}
}
