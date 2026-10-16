// Command vless-route-target is a TCP/UDP nonce fixture, not a production
// service. Run only on an internal container network with no published ports.
package main

import (
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

func main() {
	udp, err := net.ListenPacket("udp4", ":8081")
	if err != nil {
		log.Fatal(err)
	}
	go func() {
		buffer := make([]byte, 4096)
		for {
			n, source, err := udp.ReadFrom(buffer)
			if err != nil {
				return
			}
			_, _ = udp.WriteTo(buffer[:n], source)
		}
	}()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.TrimPrefix(r.URL.Path, "/"))
	})
	server := &http.Server{Addr: ":8080", ReadHeaderTimeout: 3 * time.Second, Handler: handler}
	go func() {
		log.Fatal((&http.Server{Addr: ":8082", ReadHeaderTimeout: 3 * time.Second, Handler: handler}).ListenAndServe())
	}()
	log.Fatal(server.ListenAndServe())
}
