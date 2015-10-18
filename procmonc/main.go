package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"
)

var MAGIC_PMON_HEADER []byte = []byte("PMON")

type updateContext struct {
	server  string
	conn    net.Conn
	success bool
}

func updateFile(file string, servers []string) {
	fmt.Printf("Start to update %s to servers:%v\n", file, servers)
	upc := make([]updateContext, len(servers))
	retch := make(chan int, len(servers))
	f, err := os.Open(file)
	if nil != err {
		fmt.Printf("Failed to open file:%s for reason:%v\n", file, err)
		return
	}
	defer f.Close()
	for i, server := range servers {
		//server := strings.TrimSpace(server)
		upc[i].conn, err = net.DialTimeout("tcp", server, 5*time.Second)
		if nil != err {
			upc[i].conn = nil
			fmt.Printf("Failed to connect server:%s for reason:%v\n", server, err)
			upc[i].success = false
			retch <- 1
		} else {
			go func() {
				bc := bufio.NewReader(upc[i].conn)
				for {
					line, _ := bc.ReadString('\n')
					if nil != err {
						upc[i].conn.Close()
						upc[i].conn = nil
						retch <- 1
						return
					} else {
						result := strings.TrimSpace(line)
						if strings.EqualFold(result, "success") || strings.EqualFold(result, "fail") {
							upc[i].success = strings.EqualFold(result, "success")
							retch <- 1
							return
						} else if len(result) > 0 {
							fmt.Printf("[%s]:%s\n", servers[i], result)
						}
					}
				}
			}()
		}
	}

	exitFunc := func() {
		for i := 0; i < len(servers); i++ {
			_ = <-retch
		}
		for i, ctx := range upc {
			fmt.Printf("Update %s to server:%s success:%v\n", file, servers[i], ctx.success)
			if nil == ctx.conn {
				continue
			}
			ctx.conn.Close()
		}
	}
	defer exitFunc()

	writeConns := func(p []byte) int {
		validConns := 0
		for i, ctx := range upc {
			if nil == ctx.conn {
				continue
			}
			_, err = ctx.conn.Write(p)
			if nil != err {
				fmt.Printf("Failed to write to server:%s for reason:%v", servers[i], err)
				ctx.conn.Close()
				ctx.conn = nil
			} else {
				validConns++
			}
		}
		return validConns
	}
	st, _ := f.Stat()
	size := uint64(st.Size())
	var headerBuf bytes.Buffer
	io.WriteString(&headerBuf, fmt.Sprintf("update %s\n", file))
	headerBuf.Write(MAGIC_PMON_HEADER)
	binary.Write(&headerBuf, binary.LittleEndian, size)
	writeConns(headerBuf.Bytes())
	buf := make([]byte, 8192)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			if 0 == writeConns(buf[0:n]) {
				break
			}
		}
		if nil != err {
			break
		}
	}
}

func main() {
	c := flag.Int("c", 1, "concurrent num")
	servers := flag.String("servers", "1.1.1.1:1234,1.2.2.2;1234", "remote servers, split by ','")
	file := flag.String("file", "./test.file", "update file")
	flag.Parse()

	ss := strings.Split(*servers, ",")
	for i := 0; i < len(ss); i += *c {
		n := *c
		if i+n > len(ss) {
			n = len(ss) - i
		}
		updateFile(*file, ss[i:i+n])
	}
}
