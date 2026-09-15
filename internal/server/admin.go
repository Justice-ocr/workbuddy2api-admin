package server

// Models uses the public catalog without exposing public API credentials.
func (h *Handler) Models() []map[string]any { return h.modelList() }
func (h *Handler) SetAPIKey(key string)     { h.apiKey.Store(key) }
func (h *Handler) APIKeyConfigured() bool   { return h.apiKey.Load().(string) != "" }
