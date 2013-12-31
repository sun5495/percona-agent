package test

import (
//	"fmt"
	"os"
	"time"
	proto "github.com/percona/cloud-protocol"
)


var Ts, _ = time.Parse("2006-01-02 15:04:05", "2013-12-30 18:36:00")

func WaitCmd(replyChan chan *proto.Cmd) []proto.Cmd {
	var buf []proto.Cmd
	var haveData bool = true
	for haveData {
		select {
		case cmd := <-replyChan:
			buf = append(buf, *cmd)
		case <-time.After(100 * time.Millisecond):
			haveData = false
		}
	}
	return buf
}

func WaitReply(replyChan chan *proto.Reply) []proto.Reply {
	var buf []proto.Reply
	var haveData bool = true
	for haveData {
		select {
		case reply := <-replyChan:
			buf = append(buf, *reply)
		case <-time.After(100 * time.Millisecond):
			haveData = false
		}
	}
	return buf
}

func WaitData(recvDataChan chan interface{}) []interface{} {
	var buf []interface{}
	var haveData bool = true
	for haveData {
		select {
		case data := <-recvDataChan:
			buf = append(buf, data)
		case <-time.After(10 * time.Millisecond):
			haveData = false
		}
	}
	return buf
}

func WaitLog(recvDataChan chan interface{}, n int) []proto.LogEntry {
	var buf []proto.LogEntry
	var cnt int = 0
	timeout := time.After(300 * time.Millisecond)
	FIRST_LOOP:
	for {
		select {
		case data := <-recvDataChan:
			logEntry := *data.(*proto.LogEntry)
			logEntry.Ts = Ts
			buf = append(buf, logEntry)
			cnt++
			if n > 0 && cnt >= n {
				break FIRST_LOOP
			}
		case <-timeout:
			break FIRST_LOOP
		}
	}
	if n > 0 && cnt >= n {
		SECOND_LOOP:
		for {
			select {
			case data := <-recvDataChan:
				logEntry := *data.(*proto.LogEntry)
				logEntry.Ts = Ts
				buf = append(buf, logEntry)
				cnt++
			case <-time.After(100 * time.Millisecond):
				break SECOND_LOOP
			}
		}
	}
	return buf
}

func WaitTrace(traceChan chan string) []string {
	var buf []string
	var haveData bool = true
	for haveData {
		select {
		case msg := <-traceChan:
			buf = append(buf, msg)
		case <-time.After(10 * time.Millisecond):
			haveData = false
		}
	}
	return buf
}

func WaitErr(errChan chan error) []error {
	var buf []error
	var haveData bool = true
	for haveData {
		select {
		case err := <-errChan:
			buf = append(buf, err)
		case <-time.After(10 * time.Millisecond):
			haveData = false
		}
	}
	return buf
}

func WaitFileSize(fileName string, originalSize int64) {
	var lastSize int64 = -1
	var lastChange int64 = -1
	timeout := time.After(2 * time.Second)
	TRY_LOOP:
	for {
		select {
		case <-timeout:
			break TRY_LOOP
		case <-time.After(100 * time.Millisecond):
			thisSize, err := fileSize(fileName)
			if err != nil {
				continue
			}
			if lastSize > 0 {
				thisChange := thisSize - lastSize
				//fmt.Printf("last size %d chagne %d this size %d change %d\n", lastSize, lastChange, thisSize,thisChange)
				if lastChange == 0 && thisChange == 0 {
					break TRY_LOOP
				}
				lastChange = thisChange
			}
			lastSize = thisSize
		}
	}
}

func fileSize(fileName string) (int64, error) {
	stat, err := os.Stat(fileName)
	if err != nil {
		return -1, err
	}
	return stat.Size(), nil
}
