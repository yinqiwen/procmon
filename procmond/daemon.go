package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/golang/glog"
	"io/ioutil"
)

type procConfig struct {
	Proc string
}

type procMonConfig struct {
	Listen  string
	Auth    string
	Monitor []procConfig
}

var Cfg procMonConfig

type LogWriter struct {
}

func (lw *LogWriter) Write(p []byte) (int, error) {
	glog.Infof("%s", string(p))
	return len(p), nil
}

func main() {
	conf := flag.String("conf", "./conf/procmon.json", "config file")
	flag.Parse()
	defer glog.Flush()

	data, err := ioutil.ReadFile(*conf)
	if nil != err {
		glog.Errorf("Failed to read conf for reason:%v", err)
		return
	}
	err = json.Unmarshal(data, &Cfg)
	if nil != err {
		glog.Errorf("Failed to unmarshal json to config for reason:%v", err)
		return
	}
	buildMonitorProcs()
	err = startAdminServer(Cfg.Listen)
	if nil != err {
		fmt.Printf("Bind socket failed:%v", err)
		return
	}
}
