package proxy

import (
	"bufio"
	"strings"
)

func newBufReader(s string) *bufio.Reader { return bufio.NewReader(strings.NewReader(s)) }
