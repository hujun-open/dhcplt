// common
package common

import (
	"fmt"
	"log"
	"path/filepath"
	"runtime"
)

var Logger *log.Logger

func MyLog(format string, a ...interface{}) {
	if Logger == nil {
		return
	}
	msg := fmt.Sprintf(format, a...)
	_, fname, linenum, _ := runtime.Caller(1)
	Logger.Printf("%v:%v:%v", filepath.Base(fname), linenum, msg)
}
