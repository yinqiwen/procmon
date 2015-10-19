package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/golang/glog"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"
)

type procConfig struct {
	Proc string
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
	check := func() {
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

	check()
	go func() {
		for {
			time.Sleep(5 * time.Second)
			check()
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
		glog.Errorf("%v\n", err)
		return
	}

	watchConfFile()
	err = startAdminServer(Cfg.Listen)
	if nil != err {
		fmt.Printf("Bind socket failed:%v", err)
		return
	}
}
