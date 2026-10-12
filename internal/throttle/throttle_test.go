package throttle

import (
	"testing"
	"time"
)

func TestBlocksAfterMaxFailuresAndExpires(t *testing.T) {
	now := time.Unix(1000, 0)
	l := New(3, time.Minute, 10*time.Minute)
	l.now = func() time.Time { return now }
	for i := 0; i < 2; i++ {
		l.Fail("1.2.3.4")
	}
	if b, _ := l.Blocked("1.2.3.4"); b {
		t.Fatal("blocked too early")
	}
	l.Fail("1.2.3.4")
	if b, d := l.Blocked("1.2.3.4"); !b || d != 10*time.Minute {
		t.Fatalf("not blocked: %v %v", b, d)
	}
	if b, _ := l.Blocked("5.6.7.8"); b {
		t.Fatal("other address blocked")
	}
	now = now.Add(11 * time.Minute)
	if b, _ := l.Blocked("1.2.3.4"); b {
		t.Fatal("ban did not expire")
	}
}

func TestWindowResetsAndSuccessClears(t *testing.T) {
	now := time.Unix(1000, 0)
	l := New(3, time.Minute, time.Hour)
	l.now = func() time.Time { return now }
	l.Fail("a")
	l.Fail("a")
	now = now.Add(2 * time.Minute)
	l.Fail("a") // new window: counts as 1
	if b, _ := l.Blocked("a"); b {
		t.Fatal("window did not reset")
	}
	l.Fail("a")
	l.Succeed("a")
	l.Fail("a")
	l.Fail("a")
	if b, _ := l.Blocked("a"); b {
		t.Fatal("success did not clear history")
	}
}

func TestDisabledAndNil(t *testing.T) {
	var nilL *Limiter
	nilL.Fail("a")
	if b, _ := nilL.Blocked("a"); b {
		t.Fatal("nil limiter blocked")
	}
	l := New(0, time.Minute, time.Hour)
	for i := 0; i < 10; i++ {
		l.Fail("a")
	}
	if b, _ := l.Blocked("a"); b {
		t.Fatal("disabled limiter blocked")
	}
}

func TestKey(t *testing.T) {
	if Key("1.2.3.4:5678") != "1.2.3.4" || Key("[::1]:21") != "::1" || Key("1.2.3.4") != "1.2.3.4" {
		t.Fatal("bad key")
	}
}
