package main

import (
	"context"
	"errors"
	"flag"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"sync"
	"syscall"
	"time"
)

var (
	sigChan = make(chan struct{}, 1)
	csvLock sync.Mutex

	// ErrMeterNotFound
	// is a static error to prevent heap allocations on hardware failure
	ErrMeterNotFound = errors.New("TC66C USB Meter not found. Is it plugged in?")
)

// GetUsbPort searches /dev for TC66C serial nodes.
// Prefers 'cu.*' nodes on macOS to prevent Carrier Detect open hangs.
func GetUsbPort() (string, error) {
	fsys := os.DirFS("/dev")
	commonPorts := []string{
		// Linux
		"ttyUSB*",
		"ttyACM*",
		// macOS (preferred: cu.* devices)
		"cu.usbserial*",
		"cu.wchusbserial*",
		"cu.usbmodem*",
		"cu.SLAB_USBtoUART*",
		// macOS fallback
		"tty.usbserial*",
		"tty.wchusbserial*",
		"tty.usbmodem*",
		"tty.SLAB_USBtoUART*",
	}

	for _, pattern := range commonPorts {
		matches, err := fs.Glob(fsys, pattern)
		if err != nil || len(matches) == 0 {
			continue
		}
		return "/dev/" + matches[0], nil
	}

	return "", ErrMeterNotFound
}

func main() {
	// Severely restrict the garbage collector instead of turning it off.
	// Sets a 50MB ceiling, and tells the GC to wait for the heap to grow 10x (1000%)
	// before running. This achieves near-zero CPU overhead while preventing HTTP leaks.
	debug.SetMemoryLimit(50 * 1024 * 1024)
	debug.SetGCPercent(1000)

	// Replaced fmt.Sprintf with standard string concatenation
	defaultFileName := "tc66c_data_" + time.Now().Format("20060102_150405") + ".csv"

	// Flexible flag mapping (both short and long aliases supported)
	flagFile := flag.String("file", defaultFileName, "Output CSV filename (alias: -F)")
	flag.StringVar(flagFile, "F", defaultFileName, "Output CSV filename")

	flagInterval := flag.Duration("interval", 500*time.Millisecond, "Sampling poll interval (alias: -I)")
	flag.DurationVar(flagInterval, "I", 500*time.Millisecond, "Sampling poll interval")

	flagService := flag.String("service", "", "Systemd Service name to monitor (alias: -S)")
	flag.StringVar(flagService, "S", "", "Systemd Service name to monitor")

	flagPort := flag.String("port", "", "Serial port device path (alias: -P)")
	flag.StringVar(flagPort, "P", "", "Serial port device path")

	flagPID := flag.Int("pid", -1, "Target Process ID (PID) to monitor (alias: -D)")
	flag.IntVar(flagPID, "D", -1, "Target Process ID (PID) to monitor")

	flagNoCPU := flag.Bool("nocpu", false, "Disable CPU (temp/usage/freq/cycles) logging entirely")
	flagListen := flag.String("listen", "", "Address to listen on for HTTP remote control (e.g. ':9000'). If omitted, runs immediately in direct CLI mode.")

	flag.Parse()

	port := *flagPort
	if port == "" {
		var err error
		port, err = GetUsbPort()
		if err != nil {
			fatalErr("[Hardware Error]", err)
		}
	}

	printMsg("TC66C detected on port: " + port)

	// Intercept Ctrl+C / SIGTERM cleanly
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Direct CLI Mode vs Remote HTTP Server Mode
	if *flagListen != "" {
		runHTTPServer(ctx, *flagListen, port, *flagFile, *flagInterval, *flagService, *flagPID, *flagNoCPU)
	} else {
		runCLIMode(ctx, port, *flagFile, *flagInterval, *flagService, *flagPID, *flagNoCPU)
	}
}

// Direct CLI Execution Mode: 0 network overhead, 0 background power draw.
func runCLIMode(ctx context.Context, port, fileName string, interval time.Duration, service string, pid int, noCPU bool) {
	printMsg("[Mode] Running in direct CLI mode. Press Ctrl+C to stop.")

	// Trigger shutdown when Context gets canceled
	go func() {
		<-ctx.Done()
		triggerShutdown()
	}()

	startCSVLogging(port, fileName, interval, service, pid, noCPU)
}

// HTTP Server Mode: Ideal for headless Raspberry Pi setups requiring remote endpoints.
func runHTTPServer(ctx context.Context, listenAddr, port, defaultFile string, interval time.Duration, service string, pid int, noCPU bool) {
	http.HandleFunc("/stop", func(w http.ResponseWriter, req *http.Request) {
		triggerShutdown()
		w.Write([]byte("Stopped TC66C Monitor logging\n"))
	})

	http.HandleFunc("/start", func(w http.ResponseWriter, req *http.Request) {
		csvLock.Lock()
		defer csvLock.Unlock()

		fileName := req.URL.Query().Get("file")
		if fileName == "" {
			fileName = defaultFile
		}

		reqNoCPU := noCPU
		if v := req.URL.Query().Get("nocpu"); v != "" {
			reqNoCPU = (v == "1" || v == "true")
		}

		// Gracefully stop any running worker
		triggerShutdown()
		time.Sleep(50 * time.Millisecond)

		// Drain lingering signals
		select {
		case <-sigChan:
		default:
		}

		go startCSVLogging(port, fileName, interval, service, pid, reqNoCPU)

		// Replaced fmt.Appendf with direct string concatenation cast to bytes
		w.Write([]byte("Started TC66C Monitor logging to " + fileName + "\n"))
	})

	server := &http.Server{Addr: listenAddr}

	go func() {
		<-ctx.Done()
		printMsg("\nShutting down HTTP server...")
		triggerShutdown()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		server.Shutdown(shutdownCtx)
	}()

	printMsg("[Mode] HTTP Server listening on http://localhost" + listenAddr)
	printMsg("Endpoints:\n  http://localhost" + listenAddr + "/start?file=out.csv&nocpu=1\n  http://localhost" + listenAddr + "/stop")

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fatalErr("HTTP server error", err)
	}
}

func triggerShutdown() {
	select {
	case sigChan <- struct{}{}:
	default:
	}
}

// --- HELPER FUNCTIONS ---
// printMsg replaces fmt.Println and fmt.Printf by writing directly to standard out.
func printMsg(msg string) {
	os.Stdout.WriteString(msg + "\n")
}

// fatalErr replaces log.Fatalf by writing the error to standard error and exiting.
func fatalErr(prefix string, err error) {
	os.Stderr.WriteString(prefix + ": " + err.Error() + "\n")
	os.Exit(1)
}
