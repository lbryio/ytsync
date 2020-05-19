package timing

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/lbryio/ytsync/metrics"
	"github.com/sirupsen/logrus"
)

type Timing struct {
	component    string
	milliseconds int64
	min          int64
	max          int64
	invocations  int32
}

var timings *sync.Map

func TimedComponent(component string) *Timing {
	if timings == nil {
		timings = &sync.Map{}
	}
	stored, _ := timings.LoadOrStore(component, &Timing{
		component:    component,
		milliseconds: 0,
		min:          int64(99999999),
	})
	t, _ := stored.(*Timing)
	return t
}

func ClearTimings() {
	timings.Range(func(key interface{}, value interface{}) bool {
		timings.Delete(key)
		return true
	})
}

func Report() {
	var totalTime time.Duration
	timings.Range(func(key interface{}, value interface{}) bool {
		totalTime += value.(*Timing).Get()
		return true
	})
	timings.Range(func(key interface{}, value interface{}) bool {
		component := key
		componentRuntime := value.(*Timing).Get().String()
		percentTime := float64(value.(*Timing).Get()) / float64(totalTime) * 100
		invocations := value.(*Timing).Invocations()
		avgTime := (time.Duration(int64(float64(value.(*Timing).Get()) / float64(value.(*Timing).Invocations())))).String()
		minRuntime := value.(*Timing).Min().String()
		maxRuntime := value.(*Timing).Max().String()
		logrus.Printf("component %s ran for %s (%.2f%% of the total time) - invoked %d times with an average of %s per call, a minimum of %s and a maximum of %s",
			component,
			componentRuntime,
			percentTime,
			invocations,
			avgTime,
			minRuntime,
			maxRuntime,
		)
		return true
	})
}

func (t *Timing) Add(d time.Duration) {
	metrics.Durations.WithLabelValues(t.component).Observe(d.Seconds())
	atomic.AddInt64(&t.milliseconds, d.Milliseconds())
	for {
		oldMin := atomic.LoadInt64(&t.min)
		if d.Milliseconds() < oldMin {
			if atomic.CompareAndSwapInt64(&t.min, oldMin, d.Milliseconds()) {
				break
			}
		} else {
			break
		}
	}
	for {
		oldMax := atomic.LoadInt64(&t.max)
		if d.Milliseconds() > oldMax {
			if atomic.CompareAndSwapInt64(&t.max, oldMax, d.Milliseconds()) {
				break
			}
		} else {
			break
		}
	}
	atomic.AddInt32(&t.invocations, 1)
}

func (t *Timing) Get() time.Duration {
	ms := atomic.LoadInt64(&t.milliseconds)
	return time.Duration(ms) * time.Millisecond
}

func (t *Timing) Invocations() int32 {
	return atomic.LoadInt32(&t.invocations)
}

func (t *Timing) Min() time.Duration {
	ms := atomic.LoadInt64(&t.min)
	return time.Duration(ms) * time.Millisecond
}
func (t *Timing) Max() time.Duration {
	ms := atomic.LoadInt64(&t.max)
	return time.Duration(ms) * time.Millisecond
}
