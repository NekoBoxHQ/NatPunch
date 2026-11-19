package common

import (
	"errors"
	"net"
	"testing"
	"time"
)

func TestAcceptBackoffCapsAtOneSecond(t *testing.T) {
	cases := []struct {
		failures int
		want     time.Duration
	}{
		{1, 100 * time.Millisecond},
		{2, 200 * time.Millisecond},
		{3, 400 * time.Millisecond},
		{4, 800 * time.Millisecond},
		{5, time.Second},
		{50, time.Second},
		{0, 100 * time.Millisecond},
		{-3, 100 * time.Millisecond},
	}
	for _, tc := range cases {
		if got := AcceptBackoff(tc.failures); got != tc.want {
			t.Errorf("AcceptBackoff(%d) = %v，期望 %v", tc.failures, got, tc.want)
		}
	}
}

func TestIsPermanentAcceptErr(t *testing.T) {
	permanent := []error{
		net.ErrClosed,
		errors.New("accept tcp: use of closed network connection"),
		errors.New("the mux has closed"),
		errors.New("the listener has closed"),
	}
	for _, err := range permanent {
		if !IsPermanentAcceptErr(err) {
			t.Errorf("%v 应被判定为终止错误", err)
		}
	}

	temporary := []error{
		nil,
		errors.New("accept tcp: too many open files"),
		errors.New("connection reset by peer"),
		errors.New("temporary failure"),
	}
	for _, err := range temporary {
		if IsPermanentAcceptErr(err) {
			t.Errorf("%v 不应被判定为终止错误（否则监听会永久失守）", err)
		}
	}
}
