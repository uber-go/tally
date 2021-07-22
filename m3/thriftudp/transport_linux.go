package thriftudp

import (
	"fmt"
	"io/ioutil"
	"strconv"
	"strings"
)

const _rcvbufMaxProcPath = "/proc/sys/net/core/rmem_max"

func init() {
	sysconfRcvbufMax = sysconfRcvbufMaxFromProc
}

func sysconfRcvbufMaxFromProc() (int, error) {
	buf, err := ioutil.ReadFile(_rcvbufMaxProcPath)
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(buf)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %v", _rcvbufMaxProcPath, err)
	}
	return int(n), nil
}
