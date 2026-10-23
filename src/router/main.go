// Command router is the stand-alone 443 splitter: game consoles to a `zdxsv dnas`
// process, other TLS to an https server. `zdxsv dnas` and `zdxsv all` do this
// themselves (pkg/router with the DNAS server in process); this command is for
// running dnas in a separate container, as docker-compose did.
package main

import (
	"flag"
	"log"
	"time"

	"zdxsv/pkg/router"
)

var (
	listen       = flag.String("listen", ":443", "address to listen on")
	dnasBackend  = flag.String("dnas", "dnas:443", "DNAS server for game console connections")
	httpsBackend = flag.String("https", "172.17.0.1:8444", "https server for the other connections (PROXY header); empty refuses them")
	helloTimeout = flag.Duration("hello-timeout", 3*time.Second, "how long to wait for the TLS ClientHello")
)

func main() {
	flag.Parse()
	r := &router.Router{DNASBackend: *dnasBackend, HTTPSBackend: *httpsBackend, HelloTimeout: *helloTimeout}
	log.Fatal(r.ListenAndServe(*listen))
}
