package mobile

import (
	"io"
	"net"
	"testing"
)

func TestTrafficCounting(t *testing.T) {
	var tr traffic
	raw, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := tr.wrap(raw)
	defer ln.Close()
	done := make(chan struct{})
	go func() {
		c, _ := ln.Accept()
		buf := make([]byte, 5)
		io.ReadFull(c, buf)
		c.Write([]byte("hello world"))
		c.Close()
		c.Close() // double close must not decrement twice
		close(done)
	}()
	c, _ := net.Dial("tcp", raw.Addr().String())
	c.Write([]byte("12345"))
	io.ReadAll(c)
	c.Close()
	<-done
	if tr.bytesIn.Load() != 5 || tr.bytesOut.Load() != 11 || tr.open.Load() != 0 {
		t.Fatalf("in=%d out=%d open=%d", tr.bytesIn.Load(), tr.bytesOut.Load(), tr.open.Load())
	}
}
