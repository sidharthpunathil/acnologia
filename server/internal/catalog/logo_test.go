package catalog

import (
	"context"
	"testing"
)

func TestSafeClientRefusesPrivateAddresses(t *testing.T) {
	_, _, err := DownloadImage(context.Background(), nil, "http://127.0.0.1:1/logo.png")
	if err == nil {
		t.Fatal("loopback must be refused")
	}
	_, _, err = DownloadImage(context.Background(), nil, "http://169.254.169.254/latest/meta-data")
	if err == nil {
		t.Fatal("metadata address must be refused")
	}
}

func TestImageScore(t *testing.T) {
	if ImageScore([]byte("<svg></svg>"), "image/svg+xml") < 1000 {
		t.Fatal("svg should rank highest")
	}
	ico := []byte{0, 0, 1, 0, 2, 0, 16, 16, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 48, 48, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	if ImageScore(ico, "image/x-icon") != 48 {
		t.Fatalf("ico score = %d", ImageScore(ico, "image/x-icon"))
	}
}
