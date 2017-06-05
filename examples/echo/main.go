package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"

	"github.com/acl-dev/go-master"
)

func onAccept(conn net.Conn) {
	buf := make([]byte, 8192)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			fmt.Println("read over", err)
			break
		}

		conn.Write(buf[0:n])
	}
}

func onClose(conn net.Conn) {
	log.Println("---client onClose---")
}

func main() {
	master.OnClose(onClose)
	master.OnAccept(onAccept)

	if len(os.Args) > 1 && os.Args[1] == "alone" {
		var filePath string
		flag.StringVar(&filePath, "c", "dummy.cf", "configure filePath")
		flag.Parse()

		fmt.Printf("filePath=%s, MasterServiceType=%s\r\n",
			filePath, master.MasterServiceType)
		addrs := make([]string, 1)
		if len(os.Args) > 2 {
			addrs = append(addrs, os.Args[2])
		} else {
			addrs = append(addrs, "127.0.0.1:8880")
		}

		fmt.Printf("listen:")
		for _, addr := range addrs {
			fmt.Printf(" %s", addr)
		}
		fmt.Println()

		master.NetStart(addrs)
	} else {
		// daemon mode in master framework
		master.NetStart(nil)
	}
}
