// tinyserver: 全層比較の共通 SUT。
// 仕事は「:18080 で待ち受け、GET / に 200 を返す」だけ。
// ENV LISTEN_ADDR で上書き可。go build で 5MB 程度の単一バイナリ。
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
)

func main() {
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":18080"
	}
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	log.Printf("tinyserver listening on %s pid=%d", addr, os.Getpid())
	log.Fatal(http.ListenAndServe(addr, nil))
}
