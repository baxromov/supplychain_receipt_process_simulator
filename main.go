package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	BARCODE_WS       = "wss://laundirs-supply-chain-websocket.azurewebsites.net/172.20.163.105-51236"
	SEND_WS_TEMPLATE = "wss://laundirs-supply-chain-websocket.azurewebsites.net/reader-websocket-simulator-channel-%s-local"
	ReaderMac        = "00:16:25:16:72:59"
)

var SEND_WS string

func init() {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "Unknown"
	}

	re := regexp.MustCompile(`[^A-Za-z0-9]+`)
	safeHostname := re.ReplaceAllString(hostname, "-")

	SEND_WS = fmt.Sprintf(SEND_WS_TEMPLATE, safeHostname)
	fmt.Printf("\033[32m[SEND_WS URL]: %s\033[0m\n", SEND_WS)
}

type Payload struct {
	Payload struct {
		TagReads   []map[string]interface{} `json:"tag_reads"`
		Status     string                   `json:"status_message"`
		MacAddress string                   `json:"mac_address"`
	} `json:"payload"`
}

type SimulatorOptions struct {
	Repeat       int      `json:"repeat"`
	NumberOfTags int      `json:"number_of_tags"`
	Mix          bool     `json:"mix"`
	WithBarcode  bool     `json:"with_barcode"`
	WithTags     bool     `json:"with_tags"`
	RFIDs        []string `json:"RFIDs,omitempty"`

	TargetWS string `json:"target_ws,omitempty"` // 🎯 qaysi SEND_WS ga yuboriladi
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
	return strings.ToUpper("E" + uuid.New().String())
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
		tag := map[string]interface{}{
			"epc":                 epc,
			"antennaPortNumber":   1,
			"channelInMhz":        0.0,
			"firstSeenTime":       nil,
			"lastSeenTime":        nil,
			"peakRssiInDbm":       0.0,
			"tagSeenCount":        0,
			"tid":                 "",
			"rfDopplerFrequency":  0.0,
			"phaseAngleInRadians": 0.0,
			"crc":                 0,
			"pcBits":              0,
			"gpsCoodinates": map[string]float64{
				"latitude":  0.0,
				"longitude": 0.0,
			},
			"modelDetails": map[string]interface{}{
				"modelName":          "Other",
				"userMemorySizeBits": 0,
				"epcSizeBits":        0,
				"supportsQt":         false,
			},
		}
		p.Payload.TagReads = []map[string]interface{}{tag}
		p.Payload.Status = ""
		p.Payload.MacAddress = ReaderMac

		jsonData, _ := json.Marshal(p)
		messages = append(messages, wrapMessage(string(jsonData)))
	}
	return messages
}

func wrapMessage(jsonStr string) string {
	return fmt.Sprintf("MESSAGE\ndestination:/live-track/%s\ncontent-type:application/json\n\n%s\u0000", ReaderMac, jsonStr)
}

func sendReaderData(epcs []string, withTags bool, targetWS string) {
	conn, _, err := websocket.DefaultDialer.Dial(targetWS, nil)
	if err != nil {
		log.Println("[ERROR CONNECTING TO SEND_WS]:", err)
		return
	}
	defer conn.Close()

	start := `{"payload":{"tag_reads":null,"status_message":"START","mac_address":"` + ReaderMac + `"}}`
	stop := `{"payload":{"tag_reads":null,"status_message":"STOP","mac_address":"` + ReaderMac + `"}}`

	if err := conn.WriteMessage(websocket.TextMessage, []byte(wrapMessage(start))); err != nil {
		log.Println("[ERROR SENDING START]:", err)
		return
	}
	log.Printf("[SENT TO %s]: START", targetWS)
	time.Sleep(100 * time.Millisecond)

	if withTags {
		log.Printf("[SENDING TAG MESSAGES TO %s]", targetWS)
		messages := generateMessagesForPortion(epcs)
		for _, msg := range messages {
			if err := conn.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
				log.Println("[ERROR SENDING]:", err)
				break
			}
			log.Printf("[SENT]: %.60s ...\n", msg)
			time.Sleep(100 * time.Millisecond)
		}
	} else {
		log.Println("[NO TAGS]")
	}

	if err := conn.WriteMessage(websocket.TextMessage, []byte(wrapMessage(stop))); err != nil {
		log.Println("[ERROR SENDING STOP]:", err)
	}
	log.Printf("[SENT TO %s]: STOP", targetWS)
	time.Sleep(100 * time.Millisecond)
}

func listenForBarcode(ctx context.Context, trigger chan SimulatorOptions) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			log.Println("[CONNECTING TO BARCODE SOCKET]")
			conn, _, err := websocket.DefaultDialer.Dial(BARCODE_WS, nil)
			if err != nil {
				log.Println("[BARCODE WS ERROR]", err)
				time.Sleep(2 * time.Second)
				continue
			}
			defer conn.Close()

			for {
				_, msg, err := conn.ReadMessage()
				if err != nil {
					log.Println("[ERROR READING BARCODE]", err)
					break
				}
				var parsed map[string]interface{}
				err = json.Unmarshal(msg, &parsed)
				if err != nil {
					log.Println("[ERROR PARSING BARCODE MESSAGE]", err)
					continue
				}

				data, ok := parsed["data"].(string)
				if !ok {
					continue
				}
				data = strings.ToLower(data)
				if data == "heartbeat" || data == "imager heartbeat" {
					log.Printf("[IGNORED] %s\n", data)
					continue
				}

				optMap, ok := parsed["simulator_options"].(map[string]interface{})
				if !ok {
					log.Printf("[SKIPPED]: simulator_options not provided for barcode: %s", data)
					continue
				}

				opts := SimulatorOptions{
					Repeat:       1,
					NumberOfTags: 3,
					Mix:          false,
					WithTags:     true,
					WithBarcode:  false,
					RFIDs:        []string{},
					TargetWS:     SEND_WS, // default
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
				if v, ok := optMap["target_ws"].(string); ok && v != "" {
					opts.TargetWS = v
				}

				log.Printf("[TRIGGER]: options: %+v", opts)
				trigger <- opts
			}
		}
	}
}

func main() {
	rand.Seed(time.Now().UnixNano())
	trigger := make(chan SimulatorOptions)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go listenForBarcode(ctx, trigger)

	var (
		currentEPCs    []string
		repeatCounter  = 0
		portionCounter = 1
		options        SimulatorOptions
	)

	log.Println("[WAITING FOR BARCODE TRIGGER...]")
	for {
		options = <-trigger

		if repeatCounter == 0 {
			currentEPCs = options.ResolveEPCs(currentEPCs)
			log.Printf("[PORTION #%d]: EPCs: %v", portionCounter, currentEPCs)
			portionCounter++
		}

		log.Printf("[REPEAT %d] Portion EPCs: %v", repeatCounter+1, currentEPCs)
		sendReaderData(currentEPCs, options.WithTags, options.TargetWS)

		repeatCounter++
		if repeatCounter >= options.Repeat {
			repeatCounter = 0
			log.Println("[DONE] Portion fully repeated.")
		}
		log.Println("[WAITING FOR BARCODE TRIGGER...]")
	}
}
