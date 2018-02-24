// Copyright © 2018 Enrico Stahn <enrico.stahn@gmail.com>
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package phpfpm provides convenient access to PHP-FPM pool data
package phpfpm

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/tomasen/fcgi_client"
)

// PoolProcessRequestIdle defines a process that is idle.
const PoolProcessRequestIdle string = "Idle"

// PoolProcessRequestIdle defines a process that is active.
const PoolProcessRequestActive string = "Running"

var log logger

type logger interface {
	Info(ar ...interface{})
	Infof(string, ...interface{})
	Debug(ar ...interface{})
	Debugf(string, ...interface{})
	Error(ar ...interface{})
	Errorf(string, ...interface{})
}

// PoolManager manages all configured Pools
type PoolManager struct {
	Pools []Pool `json:"pools"`
}

// Pool describes a single PHP-FPM pool that can be reached via a Socket or TCP address
type Pool struct {
	// The address of the pool, e.g. tcp://127.0.0.1:9000 or unix:///tmp/php-fpm.sock
	Address             string        `json:"-"`
	ScrapeError         error         `json:"-"`
	ScrapeFailures      int64         `json:"-"`
	Name                string        `json:"pool"`
	ProcessManager      string        `json:"process manager"`
	StartTime           timestamp     `json:"start time"`
	StartSince          int64         `json:"start since"`
	AcceptedConnections int64         `json:"accepted conn"`
	ListenQueue         int64         `json:"listen queue"`
	MaxListenQueue      int64         `json:"max listen queue"`
	ListenQueueLength   int64         `json:"listen queue len"`
	IdleProcesses       int64         `json:"idle processes"`
	ActiveProcesses     int64         `json:"active processes"`
	TotalProcesses      int64         `json:"total processes"`
	MaxActiveProcesses  int64         `json:"max active processes"`
	MaxChildrenReached  int64         `json:"max children reached"`
	SlowRequests        int64         `json:"slow requests"`
	Processes           []PoolProcess `json:"processes"`
}

// PoolProcess describes a single PHP-FPM process. A pool can have multiple processes.
type PoolProcess struct {
	PID               int64   `json:"pid"`
	State             string  `json:"state"`
	StartTime         int64   `json:"start time"`
	StartSince        int64   `json:"start since"`
	Requests          int64   `json:"requests"`
	RequestDuration   int64   `json:"request duration"`
	RequestMethod     string  `json:"request method"`
	RequestURI        string  `json:"request uri"`
	ContentLength     int64   `json:"content length"`
	User              string  `json:"user"`
	Script            string  `json:"script"`
	LastRequestCPU    float64 `json:"last request cpu"`
	LastRequestMemory int     `json:"last request memory"`
}

// Add will add a pool to the pool manager based on the given URI.
func (pm *PoolManager) Add(uri string) Pool {
	p := Pool{Address: uri}
	pm.Pools = append(pm.Pools, p)
	return p
}

// Update will run the pool.Update() method concurrently on all Pools.
func (pm *PoolManager) Update() (err error) {
	wg := &sync.WaitGroup{}

	started := time.Now()

	for idx := range pm.Pools {
		wg.Add(1)
		go func(p *Pool) {
			defer wg.Done()
			p.Update()
		}(&pm.Pools[idx])
	}

	wg.Wait()

	ended := time.Now()

	log.Debugf("Updated %v pool(s) in %v", len(pm.Pools), ended.Sub(started))

	return nil
}

// Update will connect to PHP-FPM and retrieve the latest data for the pool.
func (p *Pool) Update() (err error) {
	p.ScrapeError = nil

	env := map[string]string{
		"SCRIPT_FILENAME": "/status",
		"SCRIPT_NAME":     "/status",
		"SERVER_SOFTWARE": "go / php-fpm_exporter",
		"REMOTE_ADDR":     "127.0.0.1",
		"QUERY_STRING":    "json&full",
	}

	uri, err := url.Parse(p.Address)
	if err != nil {
		return p.error(err)
	}

	fcgi, err := fcgiclient.DialTimeout(uri.Scheme, uri.Hostname()+":"+uri.Port(), time.Duration(3)*time.Second)
	if err != nil {
		return p.error(err)
	}

	defer fcgi.Close()

	resp, err := fcgi.Get(env)
	if err != nil {
		return p.error(err)
	}

	defer resp.Body.Close()

	content, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return p.error(err)
	}

	log.Debugf("Pool[%v]: %v", p.Address, string(content))

	if err = json.Unmarshal(content, &p); err != nil {
		return p.error(err)
	}

	return nil
}

func (p *Pool) error(err error) error {
	p.ScrapeError = err
	p.ScrapeFailures++
	log.Error(err)
	return err
}

func CalculateProcessScoreboard(p Pool) (active int64, idle int64, total int64) {
	active = 0
	idle = 0
	total = 0

	for idx := range p.Processes {
		switch p.Processes[idx].State {
		case PoolProcessRequestActive:
			active++
		case PoolProcessRequestIdle:
			idle++
		default:
			log.Errorf("Unknown process state '%v'", p.Processes[idx].State)
		}
	}

	return active, idle, active + idle
}

type timestamp time.Time

// MarshalJSON customise JSON for timestamp
func (t *timestamp) MarshalJSON() ([]byte, error) {
	ts := time.Time(*t).Unix()
	stamp := fmt.Sprint(ts)
	return []byte(stamp), nil
}

// UnmarshalJSON customise JSON for timestamp
func (t *timestamp) UnmarshalJSON(b []byte) error {
	ts, err := strconv.Atoi(string(b))
	if err != nil {
		return err
	}
	*t = timestamp(time.Unix(int64(ts), 0))
	return nil
}

// SetLogger configures the used logger
func SetLogger(logger logger) {
	log = logger
}
