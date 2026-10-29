package muse

import "github.com/dtthaison/muse-mcp/internal/musepage"

// agentTexts reads the agent messages on a chat's open tab.
func (s *Service) agentTexts(chatID string) []string {
	s.mu.Lock()
	t := s.tabs[chatID]
	s.mu.Unlock()
	if t == nil {
		return nil
	}
	st, err := musepage.Snapshot(t.ctx)
	if err != nil {
		return nil
	}
	return st.AgentTexts
}

// onlyTab returns the one open tab, or nil.
func (s *Service) onlyTab() *tab {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.tabs {
		return t
	}
	return nil
}

// forgetPending drops a pending approval as a restart would, leaving the card
// on the page.
func (s *Service) forgetPending(chatID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.pending[chatID]; p != nil {
		p.timer.Stop()
		delete(s.pending, chatID)
	}
}
