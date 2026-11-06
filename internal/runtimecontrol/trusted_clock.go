package runtimecontrol

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	witnessAgreementTolerance = time.Minute
	witnessRequestTimeout     = 10 * time.Second
	clockCheckInterval        = 10 * time.Second
)

// On a machine without a battery-backed clock, systemd-timesyncd starts the
// clock from the last time it recorded (systemd-timesyncd.service(8)).
type TrustedClock struct {
	witnessOrigins       []string
	httpClient           *http.Client
	kernelDoubtsTheClock func() bool
	now                  func() time.Time
	logger               *slog.Logger
	trusted              chan struct{}
	trustOnce            sync.Once
}

type witnessReading struct {
	origin string
	offset time.Duration
	failed error
}

func NewTrustedClock(witnessEndpoints []string, logger *slog.Logger) *TrustedClock {
	return &TrustedClock{
		witnessOrigins:       witnessOriginsOf(witnessEndpoints),
		httpClient:           &http.Client{Timeout: witnessRequestTimeout},
		kernelDoubtsTheClock: kernelDoubtsTheClock,
		now:                  time.Now,
		logger:               logger,
		trusted:              make(chan struct{}),
	}
}

func (clock *TrustedClock) IsTrusted() bool {
	select {
	case <-clock.trusted:
		return true
	default:
		return false
	}
}

func (clock *TrustedClock) Trusted() <-chan struct{} {
	return clock.trusted
}

func (clock *TrustedClock) Watch(ctx context.Context) {
	ticker := time.NewTicker(clockCheckInterval)
	defer ticker.Stop()
	for checkCount := 0; ctx.Err() == nil; checkCount++ {
		readings, isTrusted := clock.check(ctx)
		if isTrusted {
			return
		}
		if checkCount == 0 {
			clock.logUntrusted(readings)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (clock *TrustedClock) check(ctx context.Context) ([]witnessReading, bool) {
	if !clock.kernelDoubtsTheClock() {
		clock.trust("kernel_synchronized", 0)
		return nil, true
	}
	if len(clock.witnessOrigins) == 0 {
		clock.startUnverified()
		return nil, true
	}
	readings := []witnessReading{}
	for _, origin := range clock.witnessOrigins {
		reading := clock.read(ctx, origin)
		if reading.failed == nil && reading.offset.Abs() <= witnessAgreementTolerance {
			clock.trust(origin, reading.offset)
			return nil, true
		}
		readings = append(readings, reading)
	}
	return readings, false
}

func (clock *TrustedClock) read(ctx context.Context, origin string) witnessReading {
	request, errorValue := http.NewRequestWithContext(ctx, http.MethodHead, origin, nil)
	if errorValue != nil {
		return witnessReading{origin: origin, failed: errorValue}
	}
	response, errorValue := clock.httpClient.Do(request)
	if errorValue != nil {
		return witnessReading{origin: origin, failed: errorValue}
	}
	response.Body.Close()
	witnessTime, errorValue := http.ParseTime(response.Header.Get("Date"))
	if errorValue != nil {
		return witnessReading{origin: origin, failed: errors.New("the answer carried no Date header")}
	}
	return witnessReading{origin: origin, offset: clock.now().Sub(witnessTime)}
}

func (clock *TrustedClock) trust(source string, offset time.Duration) {
	clock.trustOnce.Do(func() {
		clock.logger.Info("clock.trusted", "source", source, "offsetSecond", int(offset.Seconds()))
		close(clock.trusted)
	})
}

func (clock *TrustedClock) startUnverified() {
	clock.trustOnce.Do(func() {
		clock.logger.Warn("clock.unverifiable_starting_work", "reason", "the kernel reports the clock unsynchronized and no https model endpoint can witness it")
		close(clock.trusted)
	})
}

func (clock *TrustedClock) logUntrusted(readings []witnessReading) {
	witnesses := []string{}
	for _, reading := range readings {
		witnesses = append(witnesses, reading.origin+" "+reading.verdict())
	}
	clock.logger.Warn("clock.untrusted_holding_work", "witnesses", strings.Join(witnesses, "; "))
}

func (reading witnessReading) verdict() string {
	if reading.failed != nil {
		return "unreachable: " + reading.failed.Error()
	}
	return "disagrees by " + reading.offset.Round(time.Second).String()
}

func witnessOriginsOf(endpoints []string) []string {
	origins := []string{}
	seen := map[string]bool{}
	for _, endpoint := range endpoints {
		parsed, errorValue := url.Parse(strings.TrimSpace(endpoint))
		if errorValue != nil || parsed.Scheme != "https" || parsed.Host == "" {
			continue
		}
		origin := parsed.Scheme + "://" + parsed.Host
		if seen[origin] {
			continue
		}
		seen[origin] = true
		origins = append(origins, origin)
	}
	sort.Strings(origins)
	return origins
}
