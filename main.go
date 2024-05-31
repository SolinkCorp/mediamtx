// main executable.
package main

import (
	"log"
	"os"

	"net/http"

	"github.com/bluenviron/mediamtx/internal/core"

	// URI EDIT
	_ "net/http/pprof"
)

func main() {
	go func() {
		// Start pprof in the default mux so it doesn't freeze with API mux.
		log.Println(http.ListenAndServe("localhost:9988", nil))
	}()
	s, ok := core.New(os.Args[1:])
	if !ok {
		os.Exit(1)
	}
	s.Wait()
}
