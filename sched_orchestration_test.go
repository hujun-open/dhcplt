package main

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/hujun-open/dhcplt/internal/testutil"
	"github.com/hujun-open/etherconn"
	"github.com/insomniacslk/dhcp/dhcpv6"
)

// fakeClient is an in-memory dclient that records orchestration calls and
// emits dialResults, so Sched.run can be tested without any packets.
type fakeClient struct {
	id       clientID
	resultCh chan<- *dialResult

	mu             sync.Mutex
	v4Lease        bool
	v6Lease        bool
	v4Other        bool
	v6Other        bool
	dialCalls      int
	createV4Calls  int
	createV6Calls  int
	releaseV4Calls int
	releaseV6Calls int
	threeRCalls    []actionType
}

func (f *fakeClient) emit(act actionType, res execResult) {
	start := time.Now()
	f.resultCh <- &dialResult{
		action:     act,
		ExecResult: res,
		L2EP:       f.id,
		StartTime:  start,
		FinishTime: start.Add(5 * time.Millisecond),
	}
}

func (f *fakeClient) dialAll(wg *sync.WaitGroup) {
	if wg != nil {
		defer wg.Done()
	}
	f.mu.Lock()
	f.dialCalls++
	f.v4Lease = true
	f.v6Lease = true
	f.mu.Unlock()
	f.emit(actionDORA, resultSuccess)
}

func (f *fakeClient) threeRAll(_ context.Context, wg *sync.WaitGroup, act actionType) {
	if wg != nil {
		defer wg.Done()
	}
	f.mu.Lock()
	f.threeRCalls = append(f.threeRCalls, act)
	f.mu.Unlock()
	f.emit(act, resultSuccess)
}

func (f *fakeClient) createV4OtherClnt(_ actionType) error {
	f.mu.Lock()
	f.createV4Calls++
	f.v4Other = true
	f.mu.Unlock()
	return nil
}

func (f *fakeClient) createV6OtherClnt() error {
	f.mu.Lock()
	f.createV6Calls++
	f.v6Other = true
	f.mu.Unlock()
	return nil
}

func (f *fakeClient) releasev4(wg *sync.WaitGroup) error {
	if wg != nil {
		defer wg.Done()
	}
	f.mu.Lock()
	f.releaseV4Calls++
	f.mu.Unlock()
	f.emit(actionRelease, resultSuccess)
	return nil
}

func (f *fakeClient) releaseOrRenewV6(wg *sync.WaitGroup, _ dhcpv6.MessageType) error {
	if wg != nil {
		defer wg.Done()
	}
	f.mu.Lock()
	f.releaseV6Calls++
	f.mu.Unlock()
	f.emit(actionRelease, resultSuccess)
	return nil
}

func (f *fakeClient) hasV4Lease() bool     { f.mu.Lock(); defer f.mu.Unlock(); return f.v4Lease }
func (f *fakeClient) hasV6Lease() bool     { f.mu.Lock(); defer f.mu.Unlock(); return f.v6Lease }
func (f *fakeClient) hasV4OtherClnt() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.v4Other }
func (f *fakeClient) hasV6OtherClnt() bool { f.mu.Lock(); defer f.mu.Unlock(); return f.v6Other }

func (f *fakeClient) dialCount() int      { f.mu.Lock(); defer f.mu.Unlock(); return f.dialCalls }
func (f *fakeClient) releaseV4Count() int { f.mu.Lock(); defer f.mu.Unlock(); return f.releaseV4Calls }
func (f *fakeClient) createV4Count() int  { f.mu.Lock(); defer f.mu.Unlock(); return f.createV4Calls }
func (f *fakeClient) threeRActions() []actionType {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]actionType{}, f.threeRCalls...)
}

func makeFakeFactory(clients *[]*fakeClient, n int) dclientFactoryFunc {
	return func(_ *testSetup, _ exportLeaseMap, resultCh chan<- *dialResult) (map[clientID]dclient, error) {
		m := make(map[clientID]dclient)
		for i := 0; i < n; i++ {
			c := &fakeClient{id: clientID(fmt.Sprintf("c%d", i)), resultCh: resultCh}
			*clients = append(*clients, c)
			m[c.id] = c
		}
		return m, nil
	}
}

// fakeLeaseStore is an in-memory leaseStore.
type fakeLeaseStore struct {
	loaded    exportLeaseMap
	loadErr   error
	loadCalls int
	saveCalls int
}

func (s *fakeLeaseStore) load() (exportLeaseMap, error) {
	s.loadCalls++
	return s.loaded, s.loadErr
}

func (s *fakeLeaseStore) save(ctx context.Context, wg *sync.WaitGroup, _ chan *v4LeaseWithID, _ chan *v6LeaseWithID) {
	s.saveCalls++
	<-ctx.Done()
	if wg != nil {
		wg.Done()
	}
}

func fakeSetup() *testSetup {
	setup := newDefaultConf()
	setup.Ifname = testutil.FakeRelayIfName
	setup.Interval = 0
	setup.Timeout = time.Second
	return setup
}

func waitFor(t *testing.T, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", desc)
}

func sum(clients []*fakeClient, f func(*fakeClient) int) int {
	total := 0
	for _, c := range clients {
		total += f(c)
	}
	return total
}

func TestRelayFactorySeam(t *testing.T) {
	fake := testutil.NewFakeRelay()
	defer fake.Stop()

	// injected factory is used when pktRelay is unset
	setup := newDefaultConf()
	setup.relayFactory = func(*testSetup) (etherconn.PacketRelay, error) { return fake, nil }
	got, err := setup.createRelay()
	if err != nil {
		t.Fatalf("createRelay error: %v", err)
	}
	if got != fake {
		t.Errorf("createRelay() = %v, want injected relay", got)
	}

	// a pre-set pktRelay wins over the factory
	setup2 := newDefaultConf()
	setup2.pktRelay = fake
	setup2.relayFactory = func(*testSetup) (etherconn.PacketRelay, error) {
		t.Error("relayFactory should not be called when pktRelay is pre-set")
		return nil, nil
	}
	got2, err := setup2.createRelay()
	if err != nil {
		t.Fatalf("createRelay error: %v", err)
	}
	if got2 != fake {
		t.Errorf("createRelay() = %v, want pre-set relay", got2)
	}
}

// TestSetupInitWithFakeRelay exercises the previously untestable positive path
// of init by injecting an in-memory relay.
func TestSetupInitWithFakeRelay(t *testing.T) {
	relay := testutil.NewFakeRelay()
	defer relay.Stop()

	setup := newDefaultConf()
	setup.Ifname = anyInterfaceName(t)
	setup.EnableV4 = true
	setup.relayFactory = func(*testSetup) (etherconn.PacketRelay, error) { return relay, nil }

	if err := setup.init(actionDORA); err != nil {
		t.Fatalf("init() error: %v", err)
	}
	if setup.pktRelay != relay {
		t.Errorf("init() did not install the injected relay")
	}
	if setup.V6MsgType != dhcpv6.MessageTypeSolicit {
		t.Errorf("V6MsgType = %v, want SOLICIT (auto default)", setup.V6MsgType)
	}
}

func TestSchedOrchestrationDORA(t *testing.T) {
	setup := fakeSetup()
	setup.DORA.NumOfClients = 3
	var clients []*fakeClient
	setup.clientFactory = makeFakeFactory(&clients, 3)

	sch, err := NewSched(setup, actionDORA)
	if err != nil {
		t.Fatalf("NewSched error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	wg := new(sync.WaitGroup)
	wg.Add(1)
	sch.run(ctx, wg, actionDORA) // returns on its own without flapping

	if sch.summary.Total != 3 || sch.summary.Success != 3 {
		t.Errorf("summary Total/Success = %d/%d, want 3/3", sch.summary.Total, sch.summary.Success)
	}
	if got := sum(clients, (*fakeClient).dialCount); got != 3 {
		t.Errorf("dialAll calls = %d, want 3", got)
	}
}

func TestSchedOrchestrationRelease(t *testing.T) {
	setup := fakeSetup()
	setup.DORA.NumOfClients = 2
	store := &fakeLeaseStore{loaded: exportLeaseMap{
		clientID("c0"): {V4: newV4Lease()},
		clientID("c1"): {V4: newV4Lease()},
	}}
	setup.leaseStoreImpl = store
	var clients []*fakeClient
	setup.clientFactory = makeFakeFactory(&clients, 2)

	sch, err := NewSched(setup, actionRelease)
	if err != nil {
		t.Fatalf("NewSched error: %v", err)
	}
	if store.loadCalls != 1 {
		t.Errorf("leaseStore.load calls = %d, want 1", store.loadCalls)
	}

	wg := new(sync.WaitGroup)
	wg.Add(1)
	sch.run(context.Background(), wg, actionRelease)

	for _, c := range clients {
		actions := c.threeRActions()
		if len(actions) != 1 || actions[0] != actionRelease {
			t.Errorf("client %v threeRAll actions = %v, want [release]", c.id, actions)
		}
	}
	if sch.summary.Total != 2 {
		t.Errorf("summary Total = %d, want 2", sch.summary.Total)
	}
}

func TestSchedOrchestrationSaveLease(t *testing.T) {
	setup := fakeSetup()
	setup.DORA.NumOfClients = 1
	setup.DORA.SaveLease = true
	setup.EnableV4 = true
	store := &fakeLeaseStore{}
	setup.leaseStoreImpl = store
	var clients []*fakeClient
	setup.clientFactory = makeFakeFactory(&clients, 1)

	sch, err := NewSched(setup, actionDORA)
	if err != nil {
		t.Fatalf("NewSched error: %v", err)
	}
	wg := new(sync.WaitGroup)
	wg.Add(1)
	sch.run(context.Background(), wg, actionDORA)

	if store.saveCalls != 1 {
		t.Errorf("leaseStore.save calls = %d, want 1", store.saveCalls)
	}
}

func TestSchedOrchestrationFlapping(t *testing.T) {
	setup := fakeSetup()
	setup.DORA.NumOfClients = 3
	setup.DORA.Flapping.FlapNum = 2
	setup.DORA.Flapping.MinInterval = 0
	setup.DORA.Flapping.MaxInterval = time.Millisecond
	setup.DORA.Flapping.StayDownDur = 0
	var clients []*fakeClient
	setup.clientFactory = makeFakeFactory(&clients, 3)

	sch, err := NewSched(setup, actionDORA)
	if err != nil {
		t.Fatalf("NewSched error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	wg := new(sync.WaitGroup)
	wg.Add(1)
	done := make(chan struct{})
	go func() {
		sch.run(ctx, wg, actionDORA)
		close(done)
	}()

	// wait until two distinct clients have re-dialed after a release, i.e. the
	// flapping cycle is genuinely underway for both of them.
	waitFor(t, 20*time.Second, "two clients re-dialing", func() bool {
		return flappedCount(clients) >= 2
	})

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return after context cancellation")
	}

	flapped := flappedCount(clients)
	if flapped != 2 {
		t.Errorf("clients that flapped = %d, want 2", flapped)
	}
}

// flappedCount returns how many clients re-dialed after their initial DORA.
func flappedCount(clients []*fakeClient) int {
	n := 0
	for _, c := range clients {
		if c.dialCount() > 1 {
			n++
		}
	}
	return n
}
