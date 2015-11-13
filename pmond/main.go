package main

import (
	"encoding/json"
	"flag"
	"github.com/golang/glog"
	"io/ioutil"
	"os"
	"os/signal"
	"path/filepath"
	"time"
)

type checkConfig struct {
	Addr    string
	Period  int
	Timeout int
}

type procConfig struct {
	Proc   string
	LogDir string
	Env    []string
	Check  checkConfig
}

type procMonConfig struct {
	Listen    string
	Auth      string
	BackupDir string
	UploadDir string
	Monitor   []procConfig
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
			os.MkdirAll(Cfg.UploadDir, 0770)
			os.MkdirAll(Cfg.BackupDir, 0770)
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
	flag.Parse()
	defer glog.Flush()

	var err error
	confPath, err = filepath.Abs(*conf)
	if nil != err {
		glog.Errorf("%v", err)
		return
	}

	sc := make(chan os.Signal, 1)
	signal.Notify(sc, os.Interrupt, os.Kill)
	go func() {
		_ = <-sc
		killAll(&LogWriter{})
		glog.Flush()
		os.Exit(1)
	}()

	watchConfFile()
	err = startAdminServer(Cfg.Listen)
	if nil != err {
		glog.Errorf("Bind socket failed:%v", err)
		return
	}
}
