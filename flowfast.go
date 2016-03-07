/*
	Copyright (c) 2016 Christopher Young
	Distributable under the terms of The "BSD New"" License
	that can be found in the LICENSE file, herein included
	as part of this header.

	flowfast.go: Counts inputs from ADS1115, sends over a websocket.
*/

// A0 = -
// A1 = +
// 5.6K pullup on WHITE lead for FT-60.

package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/kidoman/embd"
	_ "github.com/kidoman/embd/host/all"
	_ "github.com/mattn/go-sqlite3"
	"github.com/op/go-logging"
	"github.com/paulbellamy/ratecounter"
	"golang.org/x/net/websocket"
	"math"
	"net/http"
	"os"
	"sync"
	"time"
)

const (
	GALLONS_PER_CLICK = 1 / 68000.0 // FT-60 K-factor: 68,000.
	SQLITE_DB_FILE    = "./test.db"
	LISTEN_ADDR       = ":8081"
)

type FlowStats struct {
	EvaluatedTime time.Time // Time when the counters were evaluated.
	Flow_Total    float64
	// units=gallons.
	Flow_LastSecond   float64
	Flow_LastMinute   float64
	Flow_MaxPerMinute float64
	// units=GPH.
	Flow_LastSecond_GPH      float64
	Flow_LastMinute_GPH      float64
	Flow_MaxPerMinute_GPH    float64
	Flow_LastHour_Actual_GPH float64
	// Rate counters.
	flow_total_raw   uint64
	flow_last_second *ratecounter.RateCounter
	flow_last_minute *ratecounter.RateCounter
	flow_last_hour   *ratecounter.RateCounter

	mu *sync.Mutex
}

type fuel_log struct {
	log_date_start time.Time
	log_date_end   time.Time
	flow           float64
}

var flow FlowStats

var logger = logging.MustGetLogger("flowfast")

func statusWebSocket(conn *websocket.Conn) {
	ticker := time.NewTicker(1 * time.Second)

	for {
		<-ticker.C

		flow.mu.Lock()
		updateJSON, _ := json.Marshal(&flow) //TODO.
		flow.mu.Unlock()

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

	logger.Debugf("listening on %s.\n", LISTEN_ADDR)
	err := http.ListenAndServe(LISTEN_ADDR, nil)
	if err != nil {
		logger.Errorf("can't listen on socket: %s\n", err.Error())
		os.Exit(-1)
	}
}

var i2cbus embd.I2CBus

var inputChan chan float64

// Re-calculate stats every second.
func statsCalculator() {
	ticker := time.NewTicker(1 * time.Second)
	last_update := time.Now()
	for {
		<-ticker.C
		flow.mu.Lock()

		flow.EvaluatedTime = time.Now()

		flow.Flow_Total = float64(flow.flow_total_raw) * GALLONS_PER_CLICK
		flow.Flow_LastSecond = float64(flow.flow_last_second.Rate()) * GALLONS_PER_CLICK
		flow.Flow_LastMinute = float64(flow.flow_last_minute.Rate()) * GALLONS_PER_CLICK
		flow.Flow_LastHour_Actual_GPH = float64(flow.flow_last_hour.Rate()) * GALLONS_PER_CLICK

		// Calculate maximums.
		if flow.Flow_LastMinute > flow.Flow_MaxPerMinute {
			flow.Flow_MaxPerMinute = flow.Flow_LastMinute
			flow.Flow_MaxPerMinute_GPH = flow.Flow_MaxPerMinute * float64(60.0) // Extrapolate.
		}

		// Extrapolate "GPH" numbers for the Second and Minute flow values.
		flow.Flow_LastSecond_GPH = flow.Flow_LastSecond * float64(3600.0)
		flow.Flow_LastMinute_GPH = flow.Flow_LastMinute * float64(60.0)

		// Update SQLite database.
		t := time.Now()
		logChan <- fuel_log{log_date_start: last_update, log_date_end: t, flow: flow.Flow_LastSecond}
		last_update = t

		flow.mu.Unlock()
	}
}

func processInput() {

	inputHigh := false

	for {
		mv := <-inputChan

		countCondition := false

		// 0V low.
		if math.Abs(mv-0.0) <= float64(1000.0) { // Low.
			inputHigh = false
		}

		// 5V high.
		if !inputHigh && math.Abs(mv-5000.0) <= float64(1000.0) { // High.
			inputHigh = true
			countCondition = true
			//		logger.Debugf("count! %f\n", mv)
		}

		if countCondition {
			flow.flow_total_raw++
			flow.flow_last_second.Incr(1)
			flow.flow_last_minute.Incr(1)
			flow.flow_last_hour.Incr(1)
		}
	}
}

// Ref: https://github.com/jrowberg/i2cdevlib/blob/master/Arduino/ADS1115/ADS1115.cpp
// Ref: https://github.com/jrowberg/i2cdevlib/blob/master/Arduino/ADS1115/ADS1115.h

func writeBitsW(bus embd.I2CBus, reg byte, bit_start, val_len uint, val uint16) {
	cur_val, err := bus.ReadWordFromReg(0x48, reg)
	if err != nil {
		logger.Errorf("ReadWordFromReg(): %s\n", err.Error())
		return
	}

	mask := uint16(((1 << val_len) - 1) << (bit_start - val_len + 1))
	val = val << (bit_start - val_len + 1)
	val &= mask
	cur_val &= ^(mask)
	cur_val |= val
	bus.WriteWordToReg(0x48, reg, cur_val)
}

func readADS1115() {
	inputChan = make(chan float64, 1024)

	go processInput()
	go statsCalculator()

	i2cbus = embd.NewI2CBus(1) //TODO: error checking.

	// Set up the device. ADS1115::setRate().
	writeBitsW(i2cbus, 0x01, 7, 3, 0x07)  // 3300 samples/sec.
	writeBitsW(i2cbus, 0x01, 8, 1, 0)     // MODE_CONTINUOUS.
	writeBitsW(i2cbus, 0x01, 11, 3, 0x00) // +/-6.144V. 3 mV div.
	writeBitsW(i2cbus, 0x01, 14, 3, 0x00) // setMultiplexer(MUX_P0_N1).

	for {
		v, err := i2cbus.ReadWordFromReg(0x48, 0x00)

		cv := int16(v >> 4)
		if v>>15 != 0 {
			cv = cv - 0xFFF
		}
		mv := float64(cv) * float64(3.0) // units=mV.
		if err != nil {
			logger.Errorf("ReadWordFromReg(): %s\n", err.Error())
		}

		inputChan <- mv
		time.Sleep(500 * time.Microsecond) // Oversampling.
	}

	return
}

var logChan chan fuel_log

// Logs fuel data to an SQLite database.
func dbLogger() {
	logChan = make(chan fuel_log, 1024)

	// Check if we need to create a new database.
	createDatabase := false
	if _, err := os.Stat(SQLITE_DB_FILE); os.IsNotExist(err) {
		createDatabase = true
		logger.Debugf("creating new database '%s'.\n", SQLITE_DB_FILE)
	}

	db, err := sql.Open("sqlite3", SQLITE_DB_FILE)
	if err != nil {
		logger.Errorf("sql.Open(): %s\n", err.Error())
	}
	defer db.Close()

	if createDatabase {
		createSmt := `
			CREATE TABLE fuel_flow (id INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT, log_date_start INTEGER, log_date_end INTEGER, flow REAL);
		`

		_, err = db.Exec(createSmt)
		if err != nil {
			logger.Errorf("%q: %s\n", err, createSmt)
			return
		}
	}

	for {
		f := <-logChan
		//FIXME: Timestamps here are a hack.
		q := fmt.Sprintf("INSERT INTO fuel_flow(log_date_start, log_date_end, flow) values(%d, %d, %f)", f.log_date_start.Unix(), f.log_date_end.Unix(), f.flow)
		_, err = db.Exec(q)
		if err != nil {
			logger.Errorf("stmt.Exec(): %s\n", err.Error())
		}
	}
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

	// Set up rate counters and mutex.
	flow.flow_last_second = ratecounter.NewRateCounter(1 * time.Second)
	flow.flow_last_minute = ratecounter.NewRateCounter(1 * time.Minute)
	flow.flow_last_hour = ratecounter.NewRateCounter(1 * time.Hour)
	flow.mu = &sync.Mutex{}

	go startWebListener()
	go dbLogger()
	go readADS1115()

	// Wait indefinitely.
	select {}
}
