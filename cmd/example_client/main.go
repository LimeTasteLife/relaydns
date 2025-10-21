package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"text/template"
	"time"

	"github.com/gosuda/relaydns/relaydns"
	"github.com/rs/zerolog/log"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "relaydns-client",
	Short: "RelayDNS demo client (local HTTP backend + libp2p advertiser)",
	RunE:  runClient,
}

var (
	flagServerURL      string
	flagBootstraps     []string
	flagRelay          bool
	flagBackendHTTP    string
	flagProtocol       string
	flagTopic          string
	flagAdvertiseEvery time.Duration
	flagName           string
	flagDNS            string
	flagPreferQUIC     bool
	flagPreferLocal    bool
	flagHTTPTimeout    time.Duration
)

func init() {
	flags := rootCmd.PersistentFlags()
	flags.StringVar(&flagServerURL, "server-url", "http://localhost:8080", "relayserver admin base URL (e.g. http://127.0.0.1:9090) to auto-fetch multiaddrs from /health")
	flags.StringSliceVar(&flagBootstraps, "bootstrap", nil, "multiaddrs with /p2p/ (supports /dnsaddr/ that resolves to /p2p/)")
	flags.BoolVar(&flagRelay, "relay", true, "enable libp2p relay/hole-punch support")
	flags.StringVar(&flagBackendHTTP, "backend-http", ":8081", "local backend HTTP listen address")
	flags.StringVar(&flagProtocol, "protocol", "/relaydns/http/1.0", "libp2p protocol id for streams (must match server)")
	flags.StringVar(&flagTopic, "topic", "relaydns.backends", "pubsub topic for backend adverts")
	flags.DurationVar(&flagAdvertiseEvery, "advertise-every", 3*time.Second, "interval for backend adverts")
	flags.StringVar(&flagName, "name", "demo-http", "backend display name")
	flags.StringVar(&flagDNS, "dns", "demo-http.example", "backend DNS metadata (optional)")
	flags.BoolVar(&flagPreferQUIC, "prefer-quic", true, "prefer QUIC multiaddrs when available")
	flags.BoolVar(&flagPreferLocal, "prefer-local", true, "prefer loopback/local multiaddrs when available")
	flags.DurationVar(&flagHTTPTimeout, "http-timeout", 3*time.Second, "timeout for server /health fetch")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		log.Fatal().Err(err).Msg("execute root command")
	}
}

func runClient(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1) HTTP backend
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			data := struct {
				Now  string
				Host string
				Addr string
			}{
				Now:  time.Now().Format(time.RFC1123),
				Host: r.Host,
				Addr: flagBackendHTTP,
			}
			_ = pageTmpl.Execute(w, data)
		})
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		})

		log.Info().Msgf("[client] local backend http %s", flagBackendHTTP)
		if err := http.ListenAndServe(flagBackendHTTP, mux); err != nil {
			log.Error().Err(err).Msg("[client] http backend error")
			cancel()
		}
	}()

	// 2) libp2p host
	h, err := relaydns.MakeHost(ctx, 0, flagRelay)
	if err != nil {
		return fmt.Errorf("make host: %w", err)
	}

	client, err := relaydns.NewClient(ctx, h, relaydns.ClientConfig{
		Protocol:       flagProtocol,
		Topic:          flagTopic,
		AdvertiseEvery: flagAdvertiseEvery,
		Name:           flagName,
		DNS:            flagDNS,
		TargetTCP:      addrToTarget(flagBackendHTTP),

		ServerURL:   flagServerURL,
		Bootstraps:  flagBootstraps,
		HTTPTimeout: flagHTTPTimeout,
		PreferQUIC:  flagPreferQUIC,
		PreferLocal: flagPreferLocal,
	})
	if err != nil {
		return fmt.Errorf("new client: %w", err)
	}
	defer client.Close()

	if addrs := h.Addrs(); len(addrs) > 0 {
		for _, a := range addrs {
			log.Info().Msgf("[client] host addr: %s/p2p/%s", a.String(), h.ID().String())
		}
	} else {
		log.Info().Msgf("[client] host peer: %s (no listen addrs yet)", h.ID().String())
	}

	// wait for termination
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Info().Msg("[client] shutting down")
	time.Sleep(200 * time.Millisecond)
	return nil
}

func addrToTarget(listen string) string {
	if len(listen) > 0 && listen[0] == ':' {
		return "127.0.0.1" + listen
	}
	return listen
}

var pageTmpl = template.Must(template.New("index").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
	<meta charset="UTF-8">
	<title>RelayDNS Backend</title>
	<style>
		body { font-family: sans-serif; background: #f9f9f9; padding: 40px; }
		h1 { color: #333; }
		footer { margin-top: 40px; color: #666; font-size: 0.9em; }
		.card { background: white; border-radius: 12px; padding: 24px; box-shadow: 0 2px 6px rgba(0,0,0,0.1); }
	</style>
</head>
<body>
	<div class="card">
		<h1>🚀 RelayDNS Backend</h1>
		<p>This page is served from the backend node.</p>
		<p>Current time: <b>{{.Now}}</b></p>
		<p>Hostname: <b>{{.Host}}</b></p>
	</div>
	<footer>relaydns demo client — served locally at {{.Addr}}</footer>
</body>
</html>`))
