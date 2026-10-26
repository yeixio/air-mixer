package router

import (
	"cmp"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/yeixio/air-mixer/internal/audio"
)

// defaultMaxLag is how far, in samples, one route into a destination may run
// ahead of another before the late one is treated as silent: 250 ms.
const defaultMaxLag = audio.SampleRate * audio.Channels / 4

// Router routes audio from registered sources to registered sinks. All
// audio is in the internal format (see package audio). Methods are safe for
// concurrent use.
type Router struct {
	log *slog.Logger

	mu       sync.Mutex
	running  bool
	routes   map[string]*routeState
	sources  map[string]*source
	sinks    map[string]*sink
	maxLag   int
	sequence int64 // registration order, to resolve duplicate names
}

type routeState struct {
	route   Route
	samples atomic.Int64
}

type source struct {
	id, name string
	seq      int64
	hub      *hub
}

type sink struct {
	id, name string
	seq      int64
	ch       chan<- []float32
	mixer    *audio.Mixer
	fwdDone  chan struct{}
	links    map[string]*link // by route ID
}

type link struct {
	sourceID string
	hub      *hub
	sub      *subscription
	volume   float32
}

// New returns a stopped router.
func New(logger *slog.Logger) *Router {
	if logger == nil {
		logger = slog.Default()
	}
	return &Router{
		log:     logger.With("component", "router"),
		routes:  make(map[string]*routeState),
		sources: make(map[string]*source),
		sinks:   make(map[string]*sink),
		maxLag:  defaultMaxLag,
	}
}

// Start activates routes.
func (r *Router) Start() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.running = true
	r.reconcile()
}

// Stop deactivates every route. Routes and registrations are kept.
func (r *Router) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.running = false
	r.reconcile()
}

// Running reports whether the router is started.
func (r *Router) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// AddRoute adds a route. It becomes active once its source and at least one
// destination are registered.
func (r *Router) AddRoute(route Route) error {
	if err := route.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.routes[route.ID]; ok {
		return ErrExists
	}
	r.routes[route.ID] = &routeState{route: route.clone()}
	r.reconcile()
	return nil
}

// UpdateRoute replaces the route with the same ID.
func (r *Router) UpdateRoute(route Route) error {
	if err := route.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.routes[route.ID]
	if !ok {
		return ErrNotFound
	}
	st.route = route.clone()
	r.reconcile()
	return nil
}

// RemoveRoute deletes a route and stops its audio.
func (r *Router) RemoveRoute(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.routes[id]; !ok {
		return ErrNotFound
	}
	delete(r.routes, id)
	r.reconcile()
	return nil
}

// Route returns the route with the given ID.
func (r *Router) Route(id string) (RouteStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.routes[id]
	if !ok {
		return RouteStatus{}, ErrNotFound
	}
	return r.status(st), nil
}

// Routes returns every route with its status, highest priority first.
func (r *Router) Routes() []RouteStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]RouteStatus, 0, len(r.routes))
	for _, st := range r.routes {
		out = append(out, r.status(st))
	}
	slices.SortFunc(out, func(a, b RouteStatus) int {
		return cmp.Or(cmp.Compare(b.Priority, a.Priority), cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID))
	})
	return out
}

// ActiveRoutes returns the number of routes currently delivering audio.
func (r *Router) ActiveRoutes() int {
	n := 0
	for _, st := range r.Routes() {
		if st.Active {
			n++
		}
	}
	return n
}

func (r *Router) status(st *routeState) RouteStatus {
	s := RouteStatus{
		Route:         st.route.clone(),
		ActiveDests:   []string{},
		SamplesRouted: st.samples.Load(),
	}
	s.SourceAvailable = r.sourceByName(st.route.SourceName) != nil
	for _, sk := range r.sinks {
		if _, ok := sk.links[st.route.ID]; ok {
			s.ActiveDests = append(s.ActiveDests, sk.name)
		}
	}
	slices.Sort(s.ActiveDests)
	s.Active = len(s.ActiveDests) > 0
	return s
}

// RegisterSource makes a source stream available under a name. The router
// stops reading when the channel is closed.
func (r *Router) RegisterSource(id, name string, stream <-chan []float32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.sources[id]; ok {
		r.log.Warn("source registered twice, replacing", "source", id)
		r.dropSource(old)
	}
	r.sequence++
	r.sources[id] = &source{id: id, name: name, seq: r.sequence, hub: newHub(stream)}
	r.log.Debug("source registered", "source", id, "name", name)
	r.reconcile()
}

// UnregisterSource removes a source. Routes from it become inactive.
func (r *Router) UnregisterSource(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if src, ok := r.sources[id]; ok {
		r.dropSource(src)
		r.reconcile()
	}
}

func (r *Router) dropSource(src *source) {
	delete(r.sources, src.id)
	for _, sk := range r.sinks {
		for routeID, l := range sk.links {
			if l.sourceID == src.id {
				r.unlink(sk, routeID)
			}
		}
	}
}

// RegisterSink makes a destination channel available under a name. The
// router never blocks on ch: when it is full, audio is dropped.
func (r *Router) RegisterSink(id, name string, ch chan<- []float32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.sinks[id]; ok {
		r.log.Warn("sink registered twice, replacing", "sink", id)
		r.dropSink(old)
	}
	r.sequence++
	r.sinks[id] = &sink{id: id, name: name, seq: r.sequence, ch: ch, links: make(map[string]*link)}
	r.log.Debug("sink registered", "sink", id, "name", name)
	r.reconcile()
}

// UnregisterSink removes a destination. When it returns, the router will
// not send on the sink's channel again, so the caller may close it.
func (r *Router) UnregisterSink(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if sk, ok := r.sinks[id]; ok {
		r.dropSink(sk)
	}
}

func (r *Router) dropSink(sk *sink) {
	for routeID := range sk.links {
		r.unlink(sk, routeID)
	}
	delete(r.sinks, sk.id)
}

// sourceByName returns the most recently registered source with name.
func (r *Router) sourceByName(name string) *source {
	var found *source
	for _, s := range r.sources {
		if s.name == name && (found == nil || s.seq > found.seq) {
			found = s
		}
	}
	return found
}

func (r *Router) sinkByName(name string) *sink {
	var found *sink
	for _, s := range r.sinks {
		if s.name == name && (found == nil || s.seq > found.seq) {
			found = s
		}
	}
	return found
}

// reconcile makes the active links match the routes and registrations. The
// caller holds r.mu.
func (r *Router) reconcile() {
	type want struct {
		src    *source
		volume float32
	}
	desired := make(map[string]map[string]want) // sink ID -> route ID -> link
	if r.running {
		for _, st := range r.routes {
			rt := st.route
			if !rt.Enabled {
				continue
			}
			src := r.sourceByName(rt.SourceName)
			if src == nil {
				continue
			}
			for _, name := range rt.DestNames {
				sk := r.sinkByName(name)
				if sk == nil {
					continue
				}
				if desired[sk.id] == nil {
					desired[sk.id] = make(map[string]want)
				}
				desired[sk.id][rt.ID] = want{src, rt.Volume}
			}
		}
	}

	for _, sk := range r.sinks {
		for routeID, l := range sk.links {
			w, ok := desired[sk.id][routeID]
			switch {
			case !ok || w.src.id != l.sourceID:
				r.unlink(sk, routeID)
			case w.volume != l.volume:
				l.volume = w.volume
				if err := sk.mixer.SetVolume(routeID, w.volume); err != nil {
					r.log.Debug("set volume", "route", routeID, "sink", sk.id, "err", err)
				}
			}
		}
		for routeID, w := range desired[sk.id] {
			if _, ok := sk.links[routeID]; !ok {
				r.link(sk, routeID, w.src, w.volume)
			}
		}
	}
}

func (r *Router) link(sk *sink, routeID string, src *source, volume float32) {
	if sk.mixer == nil {
		sk.mixer = audio.NewMixer(r.maxLag)
		sk.fwdDone = make(chan struct{})
		go forward(sk.mixer.Output(), sk.ch, sk.fwdDone)
	}
	sub := src.hub.subscribe(&r.routes[routeID].samples)
	if err := sk.mixer.AddInput(routeID, sub.ch, volume); err != nil {
		src.hub.unsubscribe(sub)
		r.log.Error("link route", "route", routeID, "sink", sk.id, "err", err)
		return
	}
	sk.links[routeID] = &link{sourceID: src.id, hub: src.hub, sub: sub, volume: volume}
	r.log.Info("route active", "route", routeID, "source", src.name, "dest", sk.name)
}

func (r *Router) unlink(sk *sink, routeID string) {
	l := sk.links[routeID]
	delete(sk.links, routeID)
	l.hub.unsubscribe(l.sub)
	_ = sk.mixer.RemoveInput(routeID) // already gone if the source ended
	r.log.Info("route inactive", "route", routeID, "dest", sk.name)
	if len(sk.links) == 0 {
		sk.mixer.Close()
		<-sk.fwdDone
		sk.mixer, sk.fwdDone = nil, nil
	}
}

// forward copies mixed audio to a sink without ever blocking the mixer.
func forward(in <-chan []float32, out chan<- []float32, done chan<- struct{}) {
	defer close(done)
	for buf := range in {
		select {
		case out <- buf:
		default:
		}
	}
}
