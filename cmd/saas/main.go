package main

import (
	"fmt"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

var down atomic.Bool

func main() {
	mux := http.NewServeMux()

	mux.HandleFunc("/status.json", func(w http.ResponseWriter, r *http.Request) {
		if down.Load() {
			// 応答しない → クライアントのタイムアウトを待たせる
			time.Sleep(30 * time.Second)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"status":{"indicator":"none","description":"All Systems Operational"}}`)
	})

	mux.HandleFunc("/admin/down", func(w http.ResponseWriter, r *http.Request) {
		down.Store(true)
		fmt.Fprintln(w, "SaaS is now DOWN (timeout mode)")
	})

	mux.HandleFunc("/admin/up", func(w http.ResponseWriter, r *http.Request) {
		down.Store(false)
		fmt.Fprintln(w, "SaaS is now UP")
	})

	mux.HandleFunc("/admin/status", func(w http.ResponseWriter, r *http.Request) {
		if down.Load() {
			fmt.Fprintln(w, "DOWN")
		} else {
			fmt.Fprintln(w, "UP")
		}
	})

	port := "80"
	if p := os.Getenv("PORT"); p != "" {
		port = p
	}
	fmt.Println("saas-mock listening on :" + port)
	http.ListenAndServe(":"+port, mux)
}
