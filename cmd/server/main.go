package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"duplicatecleaner/internal/web"
)

// Version 表示构建版本号，默认由构建系统通过 -ldflags 注入
var Version = "dev"

// main 是程序入口，启动 Web 服务
func main() {
	// 解析启动端口参数，默认使用不常用端口 8713
	port := flag.String("port", "8713", "Web server port, 1024-65535")
	flag.Parse()
	addr := ":" + *port
	log.Printf("DuplicateCleaner version: %s", Version)
	log.Printf("Using port: %s (override via -port)", *port)

	srv := &http.Server{
		Addr:              addr,
		Handler:           web.NewRouter(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Println(fmt.Sprintf("DuplicateCleaner web server started: http://localhost:%s", *port))
	if addrs, err := localIPv4Addrs(); err == nil {
		for _, ip := range addrs {
			log.Printf("Accessible on: http://%s:%s", ip, *port)
		}
	}
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Web server failed to start: %v", err)
	}
}

// localIPv4Addrs 返回当前机器的非回环 IPv4 地址列表
// 用于在启动日志中打印局域网可访问地址，便于同一网络中的其他设备访问
func localIPv4Addrs() ([]string, error) {
	var out []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	for _, iface := range ifaces {
		// 跳过未启用或回环接口
		if (iface.Flags&net.FlagUp) == 0 || (iface.Flags&net.FlagLoopback) != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			// 仅输出 IPv4
			if ip == nil || ip.IsLoopback() {
				continue
			}
			ip = ip.To4()
			if ip == nil {
				continue
			}
			out = append(out, ip.String())
		}
	}
	return out, nil
}
