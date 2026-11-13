package main

import (
	"testing"
	"time"
)

func TestTransferTimeouts(t *testing.T) {
	private := transferServer(nil, false)
	public := transferServer(nil, true)
	if private.ReadTimeout != 0 || private.WriteTimeout != 0 || public.WriteTimeout != 0 || public.ReadTimeout != 30*time.Second || private.ReadHeaderTimeout != 5*time.Second || private.IdleTimeout != 60*time.Second {
		t.Fatal(private, public)
	}
}
