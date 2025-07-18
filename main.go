package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	BARCODE_WS = "wss://laundirs-supply-chain-websocket.azurewebsites.net/172.20.163.105-51236"
	SEND_WS    = "wss://laundirs-supply-chain-websocket.azurewebsites.net/reader-websocket-simulator-channel-1e9a1a5e-ead4-4bdb-8453-ecb9f75594f2-local"
)

type Payload struct {
	Payload struct {
		TagReads   []map[string]interface{} `json:"tag_reads"`
		Status     string                   `json:"status_message"`
		MacAddress string                   `json:"mac_address"`
	} `json:"payload"`
}

type SimulatorOptions struct {
	Repeat       int  `json:"repeat"`
	NumberOfTags int  `json:"number_of_tags"`
	Mix          bool `json:"mix"`
	WithBarcode  bool `json:"with_barcode"`
	WithTags     bool `json:"with_tags"`
}

func generateEPC() string {
	return strings.ToUpper("E" + uuid.New().String())
}

func generatePortion(previous []string, numberOfTags int) []string {
	portion := []string{}
	needed := numberOfTags

	if len(previous) > 0 && len(previous) >= numberOfTags {
		// Reuse previous EPCs
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
		p.Payload.MacAddress = "00:16:25:16:72:59"

		jsonData, _ := json.Marshal(p)
		messages = append(messages, wrapMessage(string(jsonData)))
	}
	return messages
}

func wrapMessage(jsonStr string) string {
	return fmt.Sprintf("MESSAGE\ndestination:/live-track/00:16:25:16:72:59\ncontent-type:application/json\n\n%s\u0000", jsonStr)
}

func sendReaderData(epcs []string, withTags bool) {
	conn, _, err := websocket.DefaultDialer.Dial(SEND_WS, nil)
	if err != nil {
		log.Println("[ERROR CONNECTING TO SEND_WS]:", err)
		return
	}
	defer conn.Close()

	start := `{"payload":{"tag_reads":null,"status_message":"START","mac_address":"00:16:25:16:72:59"}}`
	stop := `{"payload":{"tag_reads":null,"status_message":"STOP","mac_address":"00:16:25:16:72:59"}}`

	err = conn.WriteMessage(websocket.TextMessage, []byte(wrapMessage(start)))
	if err != nil {
		log.Println("[ERROR SENDING START]:", err)
		return
	}
	log.Println("[SENT]: START")
	time.Sleep(100 * time.Millisecond)

	if withTags {
		log.Println("[SENDING TAG MESSAGES]")
		messages := generateMessagesForPortion(epcs)
		for _, msg := range messages {
			err := conn.WriteMessage(websocket.TextMessage, []byte(msg))
			if err != nil {
				log.Println("[ERROR SENDING]:", err)
				break
			}
			log.Printf("[SENT]: %.60s ...\n", msg)
			time.Sleep(100 * time.Millisecond)
		}
	} else {
		log.Println("[NO TAGS]")
	}

	err = conn.WriteMessage(websocket.TextMessage, []byte(wrapMessage(stop)))
	if err != nil {
		log.Println("[ERROR SENDING STOP]:", err)
	}
	log.Println("[SENT]: STOP")
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
			if options.Mix {
				currentEPCs = generatePortion(currentEPCs, options.NumberOfTags)
			} else {
				currentEPCs = generatePortion([]string{}, options.NumberOfTags)
			}
			log.Printf("[PORTION #%d]: EPCs: %v", portionCounter, currentEPCs)
			portionCounter++
		}

		log.Printf("[REPEAT %d] Portion EPCs: %v", repeatCounter+1, currentEPCs)
		sendReaderData(currentEPCs, options.WithTags)

		repeatCounter++
		if repeatCounter >= options.Repeat {
			repeatCounter = 0
			log.Println("[DONE] Portion fully repeated.")
		}
		log.Println("[WAITING FOR BARCODE TRIGGER...]")
	}
}
