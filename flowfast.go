/*
	Copyright (c) 2016 Christopher Young
	Distributable under the terms of The "BSD New"" License
	that can be found in the LICENSE file, herein included
	as part of this header.

	flowfast.go: Counts inputs from ADS1115, sends over a websocket.
*/

package main

import (
	"encoding/json"
	"github.com/kidoman/embd"
	"github.com/op/go-logging"
	"golang.org/x/net/websocket"
	"net/http"
	"os"
	"time"
)

const (
	GALLONS_PER_CLICK = 1 / 68000.0 // FT-60 K-factor: 68,000.
)

type FlowStats struct {
	Flow_Total        float64
	Flow_LastMinute   float64
	Flow_LastSecond   float64
	Flow_MaxPerMinute float64
}

var flow FlowStats

var logger = logging.MustGetLogger("flowfast")

func statusWebSocket(conn *websocket.Conn) {
	ticker := time.NewTicker(1 * time.Second)
	for {
		<-ticker.C
		updateJSON, _ := json.Marshal(&flow) //TODO.
		conn.Write(updateJSON)
	}
}

func startWebListener() {
	http.HandleFunc("/",
		func(w http.ResponseWriter, req *http.Request) {
			s := websocket.Server{
				Handler: websocket.Handler(statusWebSocket)}
			s.ServeHTTP(w, req)
		})

	logger.Debugf("listening on :8081.\n")
	err := http.ListenAndServe(":8081", nil)
	if err != nil {
		logger.Errorf("can't listen on socket: %s\n", err.Error())
		os.Exit(-1)
	}
}

var i2cbus embd.I2CBus

var inputChan chan uint16

func processInput() {
	for {
		v := <-inputChan
		logger.Debugf("%d\n", v)
		//TODO: Add stuff here to count input.
	}
}

func readADS1115() {
	inputChan = make(chan uint16, 1024)
	i2cbus = embd.NewI2CBus(1) //TODO: error checking.

	go processInput()

	for {
		v, err := i2cbus.ReadWordFromReg(0x48, 0x00)
		if err != nil {
			logger.Errorf("ReadWordFromReg(): %s\n", err.Error())
		}

		inputChan <- v
		time.Sleep(100 * time.Millisecond)
	}

	return
}

func main() {
	// Set up logging for stdout (colors).
	logBackend := logging.NewLogBackend(os.Stderr, "", 0)
	logFormat := logging.MustStringFormatter(`%{color}%{time:15:04:05.000} %{shortfunc} ▶ %{level:.4s} %{id:03x}%{color:reset} %{message}`)
	logBackendFormatter := logging.NewBackendFormatter(logBackend, logFormat)

	// Set up logging for file.
	logFileFp, err := os.OpenFile("/var/log/flowfast.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		logger.Errorf("Failed to open '%s': %s\n", "/var/log/flowfast.log", err.Error())
		return
	}
	defer logFileFp.Close()
	logFileBackend := logging.NewLogBackend(logFileFp, "", 0)
	logFileBackendFormatter := logging.NewBackendFormatter(logFileBackend, logFormat)
	logging.SetBackend(logBackendFormatter, logFileBackendFormatter)

	go startWebListener()
	//go readADS1115()

	// Wait indefinitely.
	select {}
}
