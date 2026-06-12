package main

import (
	"fmt"
	"os"
	"sync"
	"time"
)

var (
	logMu      sync.Mutex
	logLevel   = "info"
	logLoc     *time.Location
	levelOrder = map[string]int{"debug": 0, "info": 1, "warn": 2, "error": 3}
)

func initLogger(level string, loc *time.Location) {
	logLevel = level
	logLoc = loc
	if logLoc == nil {
		logLoc = time.FixedZone("WIB", 7*3600)
	}
}

func shouldLog(level string) bool {
	return levelOrder[level] >= levelOrder[logLevel]
}

func logMsg(level, format string, args ...interface{}) {
	if !shouldLog(level) {
		return
	}
	logMu.Lock()
	defer logMu.Unlock()
	ts := time.Now().In(logLoc).Format("02/01/2006, 15:04:05")
	prefix := map[string]string{
		"debug": "DBG",
		"info":  "INF",
		"warn":  "WRN",
		"error": "ERR",
	}[level]
	fmt.Fprintf(os.Stderr, "[%s] [%s] %s\n", ts, prefix, fmt.Sprintf(format, args...))
}

func logDebug(format string, args ...interface{}) { logMsg("debug", format, args...) }
func logInfo(format string, args ...interface{})  { logMsg("info", format, args...) }
func logWarn(format string, args ...interface{})  { logMsg("warn", format, args...) }
func logError(format string, args ...interface{}) { logMsg("error", format, args...) }
