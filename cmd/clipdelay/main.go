// Command clipdelay runs the ClipDelay relay: point OBS at rtmp://127.0.0.1:1935/live and change
// the delay from stdin while the stream stays up.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/rechedev9/cliphub/clipdelay/internal/relay"
)

const usage = `commands: <seconds> or a duration (30, 90s, 2m) sets the delay, live, status, quit`

func main() {
	listen := flag.String("listen", "127.0.0.1:1935", "address OBS publishes to")
	ingestKey := flag.String("ingest-key", "", "stream key OBS must use (empty accepts any)")
	initial := flag.Duration("delay", 0, "delay at start")
	retain := flag.Duration("retain", 5*time.Minute, "history kept in memory; the largest delay")
	maxMB := flag.Int64("max-mb", 512, "hard cap on buffered stream data, in MB")
	flag.Parse()

	// The RTMP library logs to the global logger, sometimes with connection details.
	logrus.SetOutput(io.Discard)
	upstream := os.Getenv("CLIPDELAY_UPSTREAM")
	if upstream == "" {
		log.Fatal("set CLIPDELAY_UPSTREAM to the platform URL with the stream key, e.g. rtmp://live.twitch.tv/app/<key>")
	}
	rl, err := relay.New(relay.Config{
		Upstream: upstream, IngestKey: *ingestKey, Delay: *initial,
		Retain: *retain, MaxBytes: *maxMB << 20, Logf: log.Printf,
	})
	if err != nil {
		log.Fatal(err)
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	log.Printf("OBS: rtmp://%s/live  (%s)", ln.Addr(), usage)
	go commands(rl, stop)
	if err := rl.Serve(ctx, ln); err != nil {
		log.Fatal(err)
	}
}

func commands(rl *relay.Relay, stop func()) {
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		line := strings.TrimSpace(in.Text())
		switch line {
		case "":
		case "quit", "exit":
			stop()
			return
		case "status":
			printStatus(rl.Status())
		case "live":
			report(rl.SetDelay(0))
		default:
			d, err := parseDelay(line)
			if err != nil {
				fmt.Println(usage)
				continue
			}
			report(rl.SetDelay(d))
		}
	}
}

func parseDelay(s string) (time.Duration, error) {
	if n, err := strconv.ParseFloat(s, 64); err == nil {
		return time.Duration(n * float64(time.Second)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, errors.New("not a delay")
	}
	return d, nil
}

func report(err error) {
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("ok")
}

func printStatus(s relay.Status) {
	r := s.Reader
	fmt.Printf("phase=%s target=%s applied=%s ready_in=%s lag=%s jumps=%d\n",
		r.Phase, r.Target, r.Applied, r.ReadyIn.Round(time.Second), r.Lag.Round(time.Millisecond), r.Jumps)
	fmt.Printf("ingest=%t buffer=%s %dMB upstream=%s reconnects=%d sent=%dMB\n",
		s.Ingesting, s.Buffer.Covered.Round(time.Second), s.Buffer.Bytes>>20,
		s.Upstream.State, s.Upstream.Reconnects, s.Upstream.BytesOut>>20)
	if s.Upstream.LastError != "" {
		fmt.Println("last error:", s.Upstream.LastError)
	}
}
