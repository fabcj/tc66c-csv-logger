package main

import (
    "flag"
    "fmt"
    "io/fs"
    "log"
    "os"
    "sync"
    "time"
    "net/http"
)

var (
    sigChan = make(chan bool, 1)
    csvLock sync.Mutex
)

// https://dev.to/rezmoss/pattern-matching-with-glob-finding-files-by-pattern-59-23lc
func GetUsbPort() (string, error) {
    var fsys = os.DirFS("/dev")
    var commonPorts = []string{"ttyUSB*", "ttyACM*"}

    for _, ports := range commonPorts {
        matches, err := fs.Glob(fsys, ports)
        if err != nil {
            continue
        }

        if len(matches) > 0 {
            var fullPath = "/dev/" + matches[0]
            return fullPath, nil
        }
    }

    return "", fmt.Errorf("TC66C USB Meter could not be found, is it plugged in?\n")
}

// https://stackoverflow.com/questions/49704456/how-to-read-from-device-when-stdin-is-pipe
func main() {
    defaultFileName := fmt.Sprintf("tc66c_data_%s.csv", time.Now().Format("20060102_150405"))

    flagFileName := flag.String("F", defaultFileName, "Name of the input file")
    flagInterval := flag.Duration("I", 500*time.Millisecond, "Sampling poll Interval")
    flagService := flag.String("S", "apache2", "Systemd Service name")
    flagPort := flag.String("P", "", "Serial port device path")
    flagtargetPID := flag.Int("D", -1, "Target Process ID (PID)")
    
    flag.Parse()

    port := *flagPort
    if port == "" {
        var err error
        port, err = GetUsbPort()
        if err != nil {
            fmt.Printf("[Hardware] Port error: %v \n", err)
            os.Exit(1)
        }
    }

    fmt.Printf("Found TC66C on port: %s\n", port)

    // https://pkg.go.dev/bufio#Reader
    tc66cPort, err := os.Open(port)
    if err != nil {
        log.Fatalf("can't open /dev/tty: %s", err)
    }

    // Verify we have access to the port
    _ = tc66cPort
    tc66cPort.Close()

    // https://ralimtek.com/posts/2019/tc66/
    // https://gobyexample.com/http-servers
    http.HandleFunc("/stop", func(w http.ResponseWriter, req *http.Request) {
        sigChan <- true
        w.Write([]byte("Stopped TC66C Monitor\n"))
    })

	http.HandleFunc("/start", func(w http.ResponseWriter, req *http.Request) {
        csvLock.Lock()
        defer csvLock.Unlock() 
        
        fileName := req.URL.Query().Get("file")
        if fileName == "" {
            fileName = *flagFileName
        }

        sigChan <- true
        go startCSVLogging(port, fileName, *flagInterval, *flagService, *flagtargetPID)
        
        responseMsg := fmt.Sprintf("Started TC66C Monitor logging to %s\n", fileName)
        w.Write([]byte(responseMsg))
    })

    fmt.Println("Server listening on :9000... Navigate to http://localhost:9000/start")
    log.Fatal(http.ListenAndServe(":9000", nil))
}