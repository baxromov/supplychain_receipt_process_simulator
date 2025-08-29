package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	// template; init() will fill with the actual IP
	BARCODE_WS_TEMPLATE = "wss://laundirs-supply-chain-websocket.azurewebsites.net/%s-51236"
	// SEND_WS_TEMPLATE stays the same, we'll fill with hostname
	SEND_WS_TEMPLATE = "wss://laundirs-supply-chain-websocket.azurewebsites.net/reader-websocket-simulator-channel-%s-local"
	ReaderMac        = "00:16:25:16:72:59"
)

var (
	// actual runtime values
	BARCODE_WS string
	SEND_WS    string

	logChan   = make(chan string, 100) // Buffered channel for logs
	upgrader  = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }} // allow origins for local UI
	templates *template.Template
)

func init() {
	// Get local IP address for BARCODE_WS (use first non-loopback IPv4)
	ip, err := getLocalIP()
	if err != nil || ip == "" {
		ip = "Unknown"
	}
	re := regexp.MustCompile(`[^A-Za-z0-9]+`)
	safeIP := re.ReplaceAllString(ip, "-")
	BARCODE_WS = fmt.Sprintf(BARCODE_WS_TEMPLATE, safeIP)

	// Get hostname for SEND_WS
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "UnknownHost"
	}
	safeHost := re.ReplaceAllString(hostname, "-")
	SEND_WS = fmt.Sprintf(SEND_WS_TEMPLATE, safeHost)

	log.Printf("[SEND_WS URL]: %s\n", SEND_WS)
	log.Printf("[BARCODE_WS URL]: %s\n", BARCODE_WS)

	// Parse HTML template
	templates = template.Must(template.New("index").Parse(indexHTML))
}

// getLocalIP retrieves the first non-loopback IPv4 address
func getLocalIP() (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
			return ipnet.IP.String(), nil
		}
	}
	return "", nil
}

// HTML template for the full-screen web UI with large logs
const indexHTML = `
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>RFID Simulator</title>
    <script src="https://cdn.tailwindcss.com"></script>
    <script>
        let ws;
        function connectWebSocket() {
            ws = new WebSocket('ws://localhost:1003/logs');
            ws.onmessage = function(event) {
                const logArea = document.getElementById('logOutput');
                logArea.value += event.data + '\n';
                logArea.scrollTop = logArea.scrollHeight;
            };
            ws.onclose = function() {
                setTimeout(connectWebSocket, 2000); // Reconnect after 2 seconds
            };
        }
        window.onload = connectWebSocket;

        async function runSimulation() {
            const repeat = document.getElementById('repeat').value;
            const numberOfTags = document.getElementById('numberOfTags').value;
            const mix = document.getElementById('mix').checked;
            const withTags = document.getElementById('withTags').checked;
            const withBarcode = document.getElementById('withBarcode').checked;
            const rfids = document.getElementById('rfids').value;
            const barcode = document.getElementById('barcode').value;

            const options = {
                repeat: parseInt(repeat) || 1,
                number_of_tags: parseInt(numberOfTags) || 3,
                mix: mix,
                with_tags: withTags,
                with_barcode: withBarcode,
                RFIDs: rfids ? rfids.split(',').map(s => s.trim()) : [],
                barcode: barcode || '',
                target_ws: '{{.SEND_WS}}'
            };

            try {
                const response = await fetch('/simulate', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify(options)
                });
                if (!response.ok) {
                    throw new Error('Simulation failed');
                }
            } catch (error) {
                console.error('Error:', error);
                document.getElementById('logOutput').value += '[ERROR] Simulation failed: ' + error.message + '\n';
            }
        }

        function copyText(elementId) {
            const text = document.getElementById(elementId).value;
            navigator.clipboard.writeText(text).then(() => {
                alert('Copied to clipboard: ' + text);
            }).catch(err => {
                console.error('Failed to copy: ', err);
                alert('Failed to copy: ' + err);
            });
        }
    </script>
</head>
<body class="bg-gray-100 h-screen flex items-center justify-center p-4">
    <div class="w-full max-w-4xl bg-white p-8 rounded-lg shadow-md h-full flex flex-col">
        <h1 class="text-3xl font-bold mb-6 text-center">RFID Simulator Configuration</h1>
        <div class="space-y-4">
            <div class="flex items-center space-x-2">
                <div class="flex-1">
                    <label class="block text-sm font-medium text-gray-700">BARCODE_WS (read-only)</label>
                    <input id="barcodeWS" type="text" value="{{.BARCODE_WS}}" readonly class="mt-1 block w-full p-2 border rounded-md bg-gray-100">
                </div>
                <button onclick="copyText('barcodeWS')" class="mt-6 bg-gray-500 text-white p-2 rounded-md hover:bg-gray-600">Copy</button>
            </div>
            <div class="flex items-center space-x-2">
                <div class="flex-1">
                    <label class="block text-sm font-medium text-gray-700">SEND_WS (read-only)</label>
                    <input id="sendWS" type="text" value="{{.SEND_WS}}" readonly class="mt-1 block w-full p-2 border rounded-md bg-gray-100">
                </div>
                <button onclick="copyText('sendWS')" class="mt-6 bg-gray-500 text-white p-2 rounded-md hover:bg-gray-600">Copy</button>
            </div>
            <div>
                <label class="block text-sm font-medium text-gray-700">Barcode</label>
                <input id="barcode" type="text" placeholder="Enter barcode (e.g., ZO-02-W-CM-RFID)" value="ZO-02-W-CM-RFID" class="mt-1 block w-full p-2 border rounded-md">
            </div>
            <div>
                <label class="block text-sm font-medium text-gray-700">Repeat Count</label>
                <input id="repeat" type="number" placeholder="Enter repeat count (e.g., 1)" value="1" class="mt-1 block w-full p-2 border rounded-md">
            </div>
            <div>
                <label class="block text-sm font-medium text-gray-700">Number of Tags</label>
                <input id="numberOfTags" type="number" placeholder="Enter number of tags (e.g., 3)" value="3" class="mt-1 block w-full p-2 border rounded-md">
            </div>
            <div>
                <label class="flex items-center">
                    <input id="mix" type="checkbox" class="mr-2">
                    <span class="text-sm font-medium text-gray-700">Mix previous EPCs</span>
                </label>
            </div>
            <div>
                <label class="flex items-center">
                    <input id="withTags" type="checkbox" checked class="mr-2">
                    <span class="text-sm font-medium text-gray-700">Send with tags</span>
                </label>
            </div>
            <div>
                <label class="flex items-center">
                    <input id="withBarcode" type="checkbox" checked class="mr-2">
                    <span class="text-sm font-medium text-gray-700">Include barcode</span>
                </label>
            </div>
            <div>
                <label class="block text-sm font-medium text-gray-700">RFIDs (optional)</label>
                <input id="rfids" type="text" placeholder="Enter RFIDs (comma-separated, optional)" class="mt-1 block w-full p-2 border rounded-md">
            </div>
            <button onclick="runSimulation()" class="w-full bg-blue-500 text-white p-2 rounded-md hover:bg-blue-600">Run Simulation</button>
        </div>
        <div class="mt-4 flex-1">
            <label class="block text-sm font-medium text-gray-700">Simulation Logs:</label>
            <textarea id="logOutput" readonly class="mt-1 block w-full h-full p-2 border rounded-md bg-gray-100 resize-none text-sm"></textarea>
        </div>
    </div>
</body>
</html>
`

type Payload struct {
	Payload struct {
		TagReads   []TagRead `json:"tag_reads"`
		Status     string    `json:"status_message"`
		MacAddress string    `json:"mac_address"`
	} `json:"payload"`
}

type TagRead struct {
	EPC                       string                 `json:"epc"`
	AntennaPortNumber         int                    `json:"antennaPortNumber"`
	ChannelInMhz              float64                `json:"channelInMhz"`
	FirstSeenTime             interface{}            `json:"firstSeenTime"`
	LastSeenTime              interface{}            `json:"lastSeenTime"`
	PeakRssiInDbm             float64                `json:"peakRssiInDbm"`
	TagSeenCount              int                    `json:"tagSeenCount"`
	Tid                       string                 `json:"tid"`
	RfDopplerFrequency        float64                `json:"rfDopplerFrequency"`
	PhaseAngleInRadians       float64                `json:"phaseAngleInRadians"`
	Crc                       int                    `json:"crc"`
	PcBits                    int                    `json:"pcBits"`
	GpsCoordinates            map[string]float64     `json:"gpsCoordinates"`
	ModelDetails              map[string]interface{} `json:"modelDetails"`
	ReadOperationResults      []interface{}          `json:"readOperationResults"`
	AntennaPortNumberPresent  bool                   `json:"antennaPortNumberPresent"`
	ChannelInMhzPresent       bool                   `json:"channelInMhzPresent"`
	FirstSeenTimePresent      bool                   `json:"firstSeenTimePresent"`
	LastSeenTimePresent       bool                   `json:"lastSeenTimePresent"`
	PeakRssiInDbmPresent      bool                   `json:"peakRssiInDbmPresent"`
	FastIdPresent             bool                   `json:"fastIdPresent"`
	RfPhaseAnglePresent       bool                   `json:"rfPhaseAnglePresent"`
	SeenCountPresent          bool                   `json:"seenCountPresent"`
	CrcPresent                bool                   `json:"crcPresent"`
	PcBitsPresent             bool                   `json:"pcBitsPresent"`
	RfDopplerFrequencyPresent bool                   `json:"rfDopplerFrequencyPresent"`
	GpsCoordinatesPresent     bool                   `json:"gpsCoordinatesPresent"`
}

type SimulatorOptions struct {
	Repeat       int      `json:"repeat"`
	NumberOfTags int      `json:"number_of_tags"`
	Mix          bool     `json:"mix"`
	WithBarcode  bool     `json:"with_barcode"`
	WithTags     bool     `json:"with_tags"`
	RFIDs        []string `json:"RFIDs"`
	Barcode      string   `json:"barcode"`
	TargetWS     string   `json:"target_ws"`
}

func (opt *SimulatorOptions) ResolveEPCs(previous []string) []string {
	var base []string
	if opt.Mix {
		base = previous
	}

	needed := opt.NumberOfTags - len(opt.RFIDs)
	if needed < 0 {
		needed = 0
	}

	generated := generatePortion(base, needed)

	all := append([]string{}, opt.RFIDs...)
	all = append(all, generated...)
	return all
}

func generateEPC() string {
	return "EC" + strings.ToUpper(uuid.New().String())
}

func generatePortion(previous []string, numberOfTags int) []string {
	portion := []string{}
	needed := numberOfTags

	if len(previous) > 0 && len(previous) >= numberOfTags {
		portion = make([]string, len(previous))
		copy(portion, previous)
		rand.Shuffle(len(portion), func(i, j int) { portion[i], portion[j] = portion[j], portion[i] })
		return portion[:numberOfTags]
	}

	if len(previous) > 0 {
		portion = make([]string, len(previous))
		copy(portion, previous)
		needed = numberOfTags - len(portion)
	}

	for i := 0; i < needed; i++ {
		newEPC := generateEPC()
		log.Printf("[NEW] Generated EPC: %s", newEPC)
		portion = append(portion, newEPC)
	}
	return portion
}

func generateMessagesForPortion(epcs []string) []string {
	var messages []string
	for _, epc := range epcs {
		p := Payload{}
		tag := TagRead{
			EPC:                 epc,
			AntennaPortNumber:   3,
			ChannelInMhz:        0.0,
			FirstSeenTime:       nil,
			LastSeenTime:        nil,
			PeakRssiInDbm:       0.0,
			TagSeenCount:        0,
			Tid:                 "",
			RfDopplerFrequency:  0.0,
			PhaseAngleInRadians: 0.0,
			Crc:                 0,
			PcBits:              0,
			GpsCoordinates: map[string]float64{
				"latitude":  0.0,
				"longitude": 0.0,
			},
			ModelDetails: map[string]interface{}{
				"modelName":          "Other",
				"userMemorySizeBits": 0,
				"epcSizeBits":        0,
				"supportsQt":         false,
			},
			ReadOperationResults:      []interface{}{},
			AntennaPortNumberPresent:  true,
			ChannelInMhzPresent:       false,
			FirstSeenTimePresent:      false,
			LastSeenTimePresent:       false,
			PeakRssiInDbmPresent:      false,
			FastIdPresent:             false,
			RfPhaseAnglePresent:       false,
			SeenCountPresent:          false,
			CrcPresent:                false,
			PcBitsPresent:             false,
			RfDopplerFrequencyPresent: false,
			GpsCoordinatesPresent:     false,
		}
		p.Payload.TagReads = []TagRead{tag}
		p.Payload.Status = ""
		p.Payload.MacAddress = ReaderMac

		jsonData, err := json.Marshal(p)
		if err != nil {
			logChan <- fmt.Sprintf("[ERROR] Failed to marshal tag JSON: %v", err)
			log.Printf("[ERROR] Failed to marshal tag JSON: %v", err)
			continue
		}
		messages = append(messages, string(jsonData))
	}
	return messages
}

func wrapMessage(jsonStr string) string {
	return fmt.Sprintf("MESSAGE\ndestination:/live-track/%s\ncontent-type:application/json\n\n%s\u0000", ReaderMac, jsonStr)
}

func sendReaderData(epcs []string, withTags bool, targetWS string) {
	conn, _, err := websocket.DefaultDialer.Dial(targetWS, nil)
	if err != nil {
		logChan <- fmt.Sprintf("[ERROR CONNECTING TO SEND_WS]: %v", err)
		log.Printf("[ERROR CONNECTING TO SEND_WS]: %v", err)
		return
	}
	defer conn.Close()

	start := Payload{
		Payload: struct {
			TagReads   []TagRead `json:"tag_reads"`
			Status     string    `json:"status_message"`
			MacAddress string    `json:"mac_address"`
		}{
			TagReads:   nil,
			Status:     "START",
			MacAddress: ReaderMac,
		},
	}
	startJSON, err := json.Marshal(start)
	if err != nil {
		logChan <- fmt.Sprintf("[ERROR MARSHALING START]: %v", err)
		log.Printf("[ERROR MARSHALING START]: %v", err)
		return
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte(wrapMessage(string(startJSON)))); err != nil {
		logChan <- fmt.Sprintf("[ERROR SENDING START]: %v", err)
		log.Printf("[ERROR SENDING START]: %v", err)
		return
	}
	logChan <- fmt.Sprintf("[SENT TO %s]: START (%s)", targetWS, wrapMessage(string(startJSON)))
	log.Printf("[SENT TO %s]: START (%s)", targetWS, string(startJSON))
	time.Sleep(100 * time.Millisecond)

	if withTags {
		logChan <- fmt.Sprintf("[SENDING TAG MESSAGES TO %s]", targetWS)
		log.Printf("[SENDING TAG MESSAGES TO %s]", targetWS)
		messages := generateMessagesForPortion(epcs)
		for _, msg := range messages {
			if err := conn.WriteMessage(websocket.TextMessage, []byte(wrapMessage(msg))); err != nil {
				logChan <- fmt.Sprintf("[ERROR SENDING]: %v", err)
				log.Printf("[ERROR SENDING]: %v", err)
				break
			}
			logChan <- fmt.Sprintf("[SENT]: %.60s ...", msg)
			log.Printf("[SENT]: %.60s ...", msg)
			time.Sleep(100 * time.Millisecond)
		}
	} else {
		logChan <- "[NO TAGS]"
		log.Println("[NO TAGS]")
	}

	stop := Payload{
		Payload: struct {
			TagReads   []TagRead `json:"tag_reads"`
			Status     string    `json:"status_message"`
			MacAddress string    `json:"mac_address"`
		}{
			TagReads:   nil,
			Status:     "STOP",
			MacAddress: ReaderMac,
		},
	}
	stopJSON, err := json.Marshal(stop)
	if err != nil {
		logChan <- fmt.Sprintf("[ERROR MARSHALING STOP]: %v", err)
		log.Printf("[ERROR MARSHALING STOP]: %v", err)
		return
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte(wrapMessage(string(stopJSON)))); err != nil {
		logChan <- fmt.Sprintf("[ERROR SENDING STOP]: %v", err)
		log.Printf("[ERROR SENDING STOP]: %v", err)
	}
	logChan <- fmt.Sprintf("[SENT TO %s]: STOP (%s)", targetWS, wrapMessage(string(stopJSON)))
	log.Printf("[SENT TO %s]: STOP (%s)", targetWS, string(stopJSON))
	time.Sleep(100 * time.Millisecond)
}

func sendHeartbeats(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			conn, _, err := websocket.DefaultDialer.Dial(BARCODE_WS, nil)
			if err != nil {
				logChan <- fmt.Sprintf("[ERROR CONNECTING TO BARCODE_WS FOR HEARTBEAT]: %v", err)
				log.Printf("[ERROR CONNECTING TO BARCODE_WS FOR HEARTBEAT]: %v", err)
				time.Sleep(2 * time.Second)
				continue
			}
			defer conn.Close()

			heartbeats := []string{`{"data":"heartbeat"}`, `{"data":"imager heartbeat"}`}
			for i := 0; ; i++ {
				select {
				case <-ctx.Done():
					return
				default:
					heartbeat := heartbeats[i%2]
					if err := conn.WriteMessage(websocket.TextMessage, []byte(heartbeat)); err != nil {
						logChan <- fmt.Sprintf("[ERROR SENDING HEARTBEAT]: %v", err)
						log.Printf("[ERROR SENDING HEARTBEAT]: %v", err)
						break
					}
					logChan <- fmt.Sprintf("[SENT HEARTBEAT TO %s]: %s", BARCODE_WS, heartbeat)
					log.Printf("[SENT HEARTBEAT TO %s]: %s", BARCODE_WS, heartbeat)
					time.Sleep(time.Duration(1000+rand.Intn(1000)) * time.Millisecond) // 1-2 seconds
				}
			}
		}
	}
}

func listenForBarcode(ctx context.Context, trigger chan SimulatorOptions) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			logChan <- "[CONNECTING TO BARCODE SOCKET]"
			log.Println("[CONNECTING TO BARCODE SOCKET]")
			conn, _, err := websocket.DefaultDialer.Dial(BARCODE_WS, nil)
			if err != nil {
				logChan <- fmt.Sprintf("[BARCODE WS ERROR]: %v", err)
				log.Printf("[BARCODE WS ERROR]: %v", err)
				time.Sleep(2 * time.Second)
				continue
			}
			defer conn.Close()

			for {
				_, msg, err := conn.ReadMessage()
				if err != nil {
					logChan <- fmt.Sprintf("[ERROR READING BARCODE]: %v", err)
					log.Printf("[ERROR READING BARCODE]: %v", err)
					break
				}
				var parsed map[string]interface{}
				err = json.Unmarshal(msg, &parsed)
				if err != nil {
					logChan <- fmt.Sprintf("[ERROR PARSING BARCODE MESSAGE]: %v", err)
					log.Printf("[ERROR PARSING BARCODE MESSAGE]: %v", err)
					continue
				}

				data, ok := parsed["data"].(string)
				if !ok {
					continue
				}
				data = strings.ToLower(data)
				if data == "heartbeat" || data == "imager heartbeat" || data == strings.ToLower("ZO-02-W-CM-RFID") {
					logChan <- fmt.Sprintf("[IGNORED]: %s", data)
					log.Printf("[IGNORED]: %s", data)
					continue
				}

				optMap, ok := parsed["simulator_options"].(map[string]interface{})
				if !ok {
					logChan <- fmt.Sprintf("[SKIPPED]: simulator_options not provided for barcode: %s", data)
					log.Printf("[SKIPPED]: simulator_options not provided for barcode: %s", data)
					continue
				}

				opts := SimulatorOptions{
					Repeat:       1,
					NumberOfTags: 3,
					Mix:          false,
					WithTags:     true,
					WithBarcode:  true,
					RFIDs:        []string{},
					Barcode:      "",
					TargetWS:     SEND_WS,
				}

				if v, ok := optMap["repeat"].(float64); ok {
					opts.Repeat = int(v)
				}
				if v, ok := optMap["number_of_tags"].(float64); ok {
					opts.NumberOfTags = int(v)
				}
				if v, ok := optMap["mix"].(bool); ok {
					opts.Mix = v
				}
				if v, ok := optMap["with_tags"].(bool); ok {
					opts.WithTags = v
				}
				if v, ok := optMap["with_barcode"].(bool); ok {
					opts.WithBarcode = v
				}
				if rawRFIDs, ok := optMap["RFIDs"].([]interface{}); ok {
					for _, raw := range rawRFIDs {
						if epc, ok := raw.(string); ok {
							opts.RFIDs = append(opts.RFIDs, epc)
						}
					}
				}
				if v, ok := optMap["barcode"].(string); ok && v != "" {
					opts.Barcode = v
				}
				if v, ok := optMap["target_ws"].(string); ok && v != "" {
					opts.TargetWS = v
				}

				logChan <- fmt.Sprintf("[TRIGGER]: options: %+v", opts)
				log.Printf("[TRIGGER]: options: %+v", opts)
				trigger <- opts
			}
		}
	}
}

func sendBarcode(barcode, targetWS string) error {
	if barcode == "" {
		logChan <- "[SKIPPED] Empty barcode input, not sending to BARCODE_WS"
		log.Println("[SKIPPED] Empty barcode input, not sending to BARCODE_WS")
		return nil
	}
	conn, _, err := websocket.DefaultDialer.Dial(targetWS, nil)
	if err != nil {
		return fmt.Errorf("connecting to %s: %v", targetWS, err)
	}
	defer conn.Close()

	message := struct {
		Data string `json:"data"`
	}{Data: barcode}
	jsonData, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("marshaling barcode JSON: %v", err)
	}

	if err := conn.WriteMessage(websocket.TextMessage, jsonData); err != nil {
		return fmt.Errorf("sending barcode: %v", err)
	}
	return nil
}

func openBrowser(url string) {
	commands := []struct {
		name string
		args []string
	}{
		{"xdg-open", []string{url}},      // Linux
		{"open", []string{url}},          // macOS
		{"google-chrome", []string{url}}, // Chrome on Linux
		{"firefox", []string{url}},       // Firefox on Linux
		{"chromium", []string{url}},      // Chromium on Linux
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	case "linux", "darwin":
		for _, c := range commands {
			if _, err := exec.LookPath(c.name); err == nil {
				cmd = exec.Command(c.name, c.args...)
				break
			}
		}
	default:
		logChan <- fmt.Sprintf("[ERROR] Unsupported platform: %s", runtime.GOOS)
		log.Printf("[ERROR] Unsupported platform: %s", runtime.GOOS)
		return
	}

	if cmd == nil {
		logChan <- fmt.Sprintf("[ERROR] No browser opener found. Please open %s manually.", url)
		log.Printf("[ERROR] No browser opener found. Please open %s manually.", url)
		return
	}

	if err := cmd.Start(); err != nil {
		logChan <- fmt.Sprintf("[ERROR] Failed to open browser: %v. Please open %s manually.", err, url)
		log.Printf("[ERROR] Failed to open browser: %v. Please open %s manually.", err, url)
		return
	}
	logChan <- fmt.Sprintf("[INFO] Opening browser to %s", url)
	log.Printf("[INFO] Opening browser to %s", url)
}

func main() {
	rand.Seed(time.Now().UnixNano())

	// Trigger channel for both UI and WebSocket
	trigger := make(chan SimulatorOptions)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start barcode listener and heartbeat sender
	go listenForBarcode(ctx, trigger)
	go sendHeartbeats(ctx)

	// Handle triggers from both UI and WebSocket
	go func() {
		var (
			currentEPCs    []string
			repeatCounter  = 0
			portionCounter = 1
		)

		for opts := range trigger {
			logChan <- fmt.Sprintf("[START] Simulation with options: %+v", opts)
			if repeatCounter == 0 {
				currentEPCs = opts.ResolveEPCs(currentEPCs)
				logChan <- fmt.Sprintf("[PORTION #%d] EPCs: %v", portionCounter, currentEPCs)
				portionCounter++
			}

			logChan <- fmt.Sprintf("[REPEAT %d] Portion EPCs: %v", repeatCounter+1, currentEPCs)
			sendReaderData(currentEPCs, opts.WithTags, opts.TargetWS)
			repeatCounter++

			if repeatCounter >= opts.Repeat {
				repeatCounter = 0
				logChan <- "[DONE] Portion fully repeated."
				log.Println("[DONE] Portion fully repeated.")
			}
			logChan <- "[WAITING FOR TRIGGER...]"
			log.Println("[WAITING FOR TRIGGER...]")
		}
	}()

	// HTTP handlers
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		data := struct {
			BARCODE_WS string
			SEND_WS    string
		}{
			BARCODE_WS: BARCODE_WS,
			SEND_WS:    SEND_WS,
		}
		if err := templates.ExecuteTemplate(w, "index", data); err != nil {
			http.Error(w, "Failed to render template", http.StatusInternalServerError)
			logChan <- fmt.Sprintf("[ERROR] Template rendering: %v", err)
			log.Printf("[ERROR] Template rendering: %v", err)
		}
	})

	http.HandleFunc("/simulate", func(w http.ResponseWriter, r *http.Request) {
		var opts SimulatorOptions
		if err := json.NewDecoder(r.Body).Decode(&opts); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			logChan <- fmt.Sprintf("[ERROR] Invalid request body: %v", err)
			log.Printf("[ERROR] Invalid request body: %v", err)
			return
		}

		// Send barcode to BARCODE_WS if provided
		if opts.Barcode != "" {
			barcodeMsg := struct {
				Data string `json:"data"`
			}{Data: opts.Barcode}
			jsonData, _ := json.Marshal(barcodeMsg)
			if err := sendBarcode(opts.Barcode, BARCODE_WS); err != nil {
				logChan <- fmt.Sprintf("[ERROR SENDING BARCODE TO %s]: %v", BARCODE_WS, err)
				log.Printf("[ERROR SENDING BARCODE TO %s]: %v", BARCODE_WS, err)
			} else {
				logChan <- fmt.Sprintf("[SENT BARCODE TO %s]: %s", BARCODE_WS, jsonData)
				log.Printf("[SENT BARCODE TO %s]: %s", BARCODE_WS, jsonData)
			}
		}

		trigger <- opts
		w.WriteHeader(http.StatusOK)
	})

	http.HandleFunc("/logs", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			logChan <- fmt.Sprintf("[ERROR] WebSocket upgrade: %v", err)
			log.Printf("[ERROR] WebSocket upgrade: %v", err)
			return
		}
		defer conn.Close()

		for {
			select {
			case logMsg := <-logChan:
				if err := conn.WriteMessage(websocket.TextMessage, []byte(logMsg)); err != nil {
					logChan <- fmt.Sprintf("[ERROR] WebSocket write: %v", err)
					log.Printf("[ERROR] WebSocket write: %v", err)
					return
				}
			case <-ctx.Done():
				return
			}
		}
	})

	// Start HTTP server
	go func() {
		logChan <- "[STARTING SERVER] http://localhost:1003"
		log.Println("[STARTING SERVER] http://localhost:1003")
		if err := http.ListenAndServe(":1003", nil); err != nil {
			logChan <- fmt.Sprintf("[ERROR] Server failed: %v", err)
			log.Fatalf("[ERROR] Server failed: %v", err)
		}
	}()

	// Open browser
	go openBrowser("http://localhost:1003")

	// Keep the program running
	select {}
}