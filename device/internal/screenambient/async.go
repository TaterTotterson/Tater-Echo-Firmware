package screenambient

import (
	"sync"
	"time"
)

type ambientSample struct {
	lux float64
	ok  bool
	err error
}

type asyncReader struct {
	name    string
	stop    chan struct{}
	results chan ambientSample
	once    sync.Once
}

// NonBlocking moves device reads off the display loop. Some IIO sysfs drivers
// wait a full sensor conversion on read, even when the UI only needs the latest
// brightness estimate. One worker owns the sensor, with at most one queued
// result. Close never races the driver's Sample/Close calls or waits for I/O.
func NonBlocking(reader Reader, period time.Duration) Reader {
	if period <= 0 {
		period = 500 * time.Millisecond
	}
	a := &asyncReader{name: reader.Name(), stop: make(chan struct{}), results: make(chan ambientSample, 1)}
	go func() {
		defer reader.Close()
		ticker := time.NewTicker(period)
		defer ticker.Stop()
		for {
			select {
			case <-a.stop:
				return
			default:
			}
			lux, ok, err := reader.Sample()
			value := ambientSample{lux, ok, err}
			select {
			case <-a.stop:
				return
			default:
			}
			select {
			case a.results <- value:
			default:
				select {
				case <-a.results:
				default:
				}
				select {
				case a.results <- value:
				case <-a.stop:
					return
				}
			}
			select {
			case <-a.stop:
				return
			case <-ticker.C:
			}
		}
	}()
	return a
}

func (a *asyncReader) Name() string { return a.name }
func (a *asyncReader) Close() error {
	a.once.Do(func() { close(a.stop) })
	return nil
}
func (a *asyncReader) Sample() (float64, bool, error) {
	select {
	case <-a.stop:
		return 0, false, nil
	default:
	}
	select {
	case sample := <-a.results:
		return sample.lux, sample.ok, sample.err
	default:
		return 0, false, nil
	}
}
