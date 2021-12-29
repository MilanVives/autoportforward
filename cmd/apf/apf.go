package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/ruoshan/autoportforward/bootstrap"
	"github.com/ruoshan/autoportforward/logger"
	"github.com/ruoshan/autoportforward/manager"
	"github.com/ruoshan/autoportforward/mux"
	"github.com/ruoshan/autoportforward/proxy"
)

var log = logger.GetNullLogger()

func sigHandler(fn func()) {
	c := make(chan os.Signal)
	signal.Notify(c, os.Interrupt)
	go func() {
		<-c
		fn()
	}()
}

var isK8s = flag.Bool("k", false, "proxy for Kubernetes pod")
var dbg = flag.Bool("d", false, "log debug info to /tmp/autoportforward.log")

func init() {
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), `Usage:
    * apf {container ID}
    * apf -k {namespace}/{pod ID}
Flags:`)
		flag.PrintDefaults()
	}
}

func main() {
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(1)
	}
	containerId := flag.Arg(0)

	if *dbg {
		log = logger.GetLogger()
	}

	// Bootstrap: docker cp the tar archive
	msg, err := bootstrap.Bootstrap(*isK8s, containerId)
	if err != nil {
		panic(fmt.Sprintf("Failed to bootstrap: %s", msg))
	}

	var cmd []string
	if *isK8s {
		splits := strings.SplitN(containerId, "/", 2)
		cmd = []string{"kubectl", "exec", "-i", "-n", splits[0], splits[1], "/apf-agent"}
	} else {
		cmd = []string{"docker", "exec", "-i", containerId, "/apf-agent"}
	}
	if *dbg {
		cmd = append(cmd, "-d")
	}

	ms := mux.NewCmdPipeMuxServer(cmd[0], cmd[1:]...)
	if ms == nil {
		panic("Failed to create mux server")
	}
	sigHandler(func() {
		ms.Shutdown()
	})

	// Open two streams for manager. NB: the order of Accept() is different from Connect() in the remote agent
	mgrReceivingStream, _ := ms.Accept()
	mgrSendingStream, _ := ms.Accept()
	mgr := manager.NewManager(mgrReceivingStream, mgrSendingStream, log, func() {
		ms.Close()
	})

	pl := proxy.NewProxyListener(ms, log)
	if pl == nil {
		panic("Failed to create proxy listener")
	}
	mgr.SetCallbacks(pl.NewListener, pl.CloseListener)
	mgr.Run()

	pf := proxy.NewProxyForwarder(ms, log)
	if pf == nil {
		panic("Failed to create proxy forwarder")
	}
	pf.Start()
}
