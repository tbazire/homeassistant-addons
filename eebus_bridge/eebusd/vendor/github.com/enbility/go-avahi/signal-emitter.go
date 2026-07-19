package avahi

func (c *Server) signalEmitterFree(e SignalEmitter) {
	o := e.GetObjectPath()

	c.mutex.Lock()
	defer c.mutex.Unlock()

	_, ok := c.signalEmitters[o]
	if ok {
		delete(c.signalEmitters, o)
	}

	e.Free()
}
