package domain

import (
	"errors"
	"math"
	"testing"
)

func TestChargeBytesVectors(t *testing.T) {
	cases := []struct {
		raw   int64
		milli int
		want  int64
	}{
		{0, 1500, 0},
		{-5, 1500, 0},
		{1000, 1000, 1000},
		{math.MaxInt64, 1000, math.MaxInt64}, // identity is exact at the default
		{1000, 1500, 1500},
		{1000, 500, 500},
		{1000, 0, 0},
		{1, 1500, 2}, // 1.5 rounds half up
		{1, 1499, 1}, // 1.499 rounds down
		{3, 333, 1},  // 0.999
		{1, 100000, 100},
		{123_456_789, 1250, 154_320_986}, // 154320986.25
		{math.MaxInt64, 100000, math.MaxInt64},
		{math.MaxInt64 / 50, 100000, math.MaxInt64}, // saturates instead of wrapping
	}
	for _, c := range cases {
		if got := ChargeBytes(c.raw, c.milli); got != c.want {
			t.Errorf("ChargeBytes(%d, %d) = %d, want %d", c.raw, c.milli, got, c.want)
		}
	}
}

func TestRawBytesForFloorsAndRefusesFree(t *testing.T) {
	cases := []struct {
		charged int64
		milli   int
		want    int64
		ok      bool
	}{
		{1500, 1500, 1000, true},
		{1501, 1500, 1000, true}, // 1000.67 floors
		{1499, 1500, 999, true},
		{1000, 500, 2000, true},
		{1000, 1000, 1000, true},
		{0, 1500, 0, true},
		{-1, 1500, 0, true},
		{99, 100000, 0, true},
		{1000, 0, 0, false},
		{math.MaxInt64, 1, math.MaxInt64, true}, // saturates
		{math.MaxInt64, 1000, math.MaxInt64, true},
	}
	for _, c := range cases {
		got, ok := RawBytesFor(c.charged, c.milli)
		if got != c.want || ok != c.ok {
			t.Errorf("RawBytesFor(%d, %d) = %d, %v; want %d, %v", c.charged, c.milli, got, ok, c.want, c.ok)
		}
	}
}

func TestRawHeadroomVectors(t *testing.T) {
	cases := []struct {
		name     string
		headroom int64
		milli    int
		want     int64
	}{
		{"unlimited stays unlimited", 0, 1500, 0},
		{"negative is unlimited", -3, 1500, 0},
		{"exhausted sentinel kept", 1, 1500, 1},
		{"exhausted sentinel kept on a cheap server", 1, 500, 1},
		{"exhausted sentinel kept on a free server", 1, 0, 1},
		{"default factor is identity", 5000, 1000, 5000},
		{"expensive server allows fewer raw bytes", 3000, 1500, 2000},
		{"rounds down", 3001, 1500, 2000},
		{"cheap server allows more raw bytes", 3000, 500, 6000},
		{"never rounds a remainder to unlimited", 50, 100000, 1},
		{"free server with quota left is not gated", 3000, 0, 0},
		{"saturates", math.MaxInt64, 1, math.MaxInt64},
	}
	for _, c := range cases {
		if got := RawHeadroom(c.headroom, c.milli); got != c.want {
			t.Errorf("%s: RawHeadroom(%d, %d) = %d, want %d", c.name, c.headroom, c.milli, got, c.want)
		}
	}
}

func TestValidateTrafficFactor(t *testing.T) {
	for _, ok := range []int{0, 1, 1000, TrafficFactorMax} {
		if err := ValidateTrafficFactor(ok); err != nil {
			t.Errorf("ValidateTrafficFactor(%d) = %v", ok, err)
		}
	}
	for _, bad := range []int{-1, TrafficFactorMax + 1} {
		if err := ValidateTrafficFactor(bad); !errors.Is(err, ErrValidation) {
			t.Errorf("ValidateTrafficFactor(%d) = %v, want ErrValidation", bad, err)
		}
	}
}
