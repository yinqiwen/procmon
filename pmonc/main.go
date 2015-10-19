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
var EXEC_SUCCESS string = "PMON_SUCCESS"
var EXEC_FAIL string = "PMON_FAIL"

var connAuthToken string

type serverContext struct {
	server  string
	conn    net.Conn
	success bool
}

type clientContext struct {
	action  string
	servers []string
	sctxs   []serverContext
	rch     chan int
}

func (ctx *clientContext) Write(p []byte) (int, error) {
	validConns := 0
	for _, sctx := range ctx.sctxs {
		if nil == sctx.conn {
			continue
		}
		_, err := sctx.conn.Write(p)
		if nil != err {
			fmt.Printf("Failed to write to server:%s for reason:%v", sctx.server, err)
			sctx.conn.Close()
		} else {
			validConns++
		}
	}
	if 0 == validConns {
		return 0, fmt.Errorf("No connected servers")
	}
	return len(p), nil
}

func (ctx *clientContext) close() {
	for _, sctx := range ctx.sctxs {
		if nil == sctx.conn {
			continue
		}
		sctx.conn.Close()
	}
}

func (ctx *clientContext) print() {
	for i := 0; i < len(ctx.servers); i++ {
		_ = <-ctx.rch
	}
	for _, sctx := range ctx.sctxs {
		fmt.Printf("[%s] %s success:%v\n", sctx.server, ctx.action, sctx.success)
	}
}

func buildClientContext(servers []string) *clientContext {
	ctx := new(clientContext)
	ctx.servers = servers
	ctx.sctxs = make([]serverContext, len(servers))
	ctx.rch = make(chan int, len(servers))
	var err error
	for i, server := range servers {
		ctx.sctxs[i].server = server
		ctx.sctxs[i].conn, err = net.DialTimeout("tcp", server, 5*time.Second)
		if nil != err {
			ctx.sctxs[i].conn = nil
			fmt.Printf("Failed to connect server:%s for reason:%v\n", server, err)
			ctx.sctxs[i].success = false
			ctx.rch <- 1
		} else {
			if len(connAuthToken) > 0 {
				ctx.sctxs[i].conn.Write([]byte(connAuthToken + "\r\n"))
			}
			go func(index int) {
				bc := bufio.NewReader(ctx.sctxs[index].conn)
				for {
					line, err := bc.ReadString('\n')
					if nil != err {
						ctx.sctxs[index].conn.Close()
						ctx.rch <- 1
						break
					} else {
						result := strings.TrimSpace(line)
						if result == EXEC_SUCCESS || result == EXEC_FAIL {
							ctx.sctxs[index].success = result == EXEC_SUCCESS
							ctx.sctxs[index].conn.Close()
							ctx.rch <- 1
							return
						} else if len(result) > 0 {
							fmt.Printf("[%s]:%s\n", servers[index], result)
						}
					}
				}
			}(i)
		}
	}
	return ctx
}

func exec(cmd string, ctx *clientContext) {
	fmt.Printf("Start to send/exec '%s' to servers:%v\n", cmd, ctx.servers)
	defer ctx.print()
	io.WriteString(ctx, fmt.Sprintf("%s\r\n", cmd))
}

func execCmd(cmd string, ctx *clientContext) {
	fmt.Printf("Start to exec '%s' to servers:%v\n", cmd, ctx.servers)
	defer ctx.print()
	io.WriteString(ctx, fmt.Sprintf("system %s\r\n", cmd))
}

func updateFile(file string, ctx *clientContext) {
	fmt.Printf("Start to update %s to servers:%v\n", file, ctx.servers)
	f, err := os.Open(file)
	if nil != err {
		fmt.Printf("Failed to open file:%s for reason:%v\n", file, err)
		ctx.close()
		return
	}
	defer f.Close()
	defer ctx.print()
	st, _ := f.Stat()
	size := uint64(st.Size())
	var headerBuf bytes.Buffer
	io.WriteString(&headerBuf, fmt.Sprintf("upload %s\n", file))
	headerBuf.Write(MAGIC_PMON_HEADER)
	binary.Write(&headerBuf, binary.LittleEndian, size)
	headerBuf.WriteTo(ctx)
	//ctx.Write(headerBuf.Bytes())
	_, err = io.Copy(ctx, f)
	if nil != err {
		ctx.close()
	}
}

func main() {
	c := flag.Int("c", 1, "concurrent num")
	servers := flag.String("servers", "1.1.1.1:60000,1.2.2.2:60000", "remote servers, split by ','")
	file := flag.String("upload", "", "upload <file>")
	cmd := flag.String("cmd", "", "remote command execute")
	auth := flag.String("auth", "", "connection auth token")
	flag.Parse()
	connAuthToken = *auth
	if len(*file) == 0 && len(*cmd) == 0 {
		fmt.Printf("'-upload' or '-cmd' need to be sepecified in args.\n")
		flag.PrintDefaults()
		return
	}
	ss := strings.Split(*servers, ",")
	for i := 0; i < len(ss); i += *c {
		n := *c
		if i+n > len(ss) {
			n = len(ss) - i
		}

		if len(*file) > 0 {
			ctx := buildClientContext(ss[i : i+n])
			ctx.action = "upload file"
			updateFile(*file, ctx)
			ctx.close()
		}
		if len(*cmd) > 0 {
			ctx := buildClientContext(ss[i : i+n])
			ctx.action = "exec command"
			exec(*cmd, ctx)
			ctx.close()
		}
	}
}
