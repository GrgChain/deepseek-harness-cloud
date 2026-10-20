package modelgateway

import (
	"encoding/json"
	"strings"
	"time"
)

const (
	responseReasoningTTL        = 15 * time.Minute
	responseReasoningMaxEntries = 2048
	responseReasoningMaxBytes   = 16 << 20
	responseReasoningMaxEntry   = 1 << 20
	responseReasoningMaxReplay  = 8 << 20
)

type cachedResponseReasoning struct {
	items   []json.RawMessage
	bytes   int
	expires time.Time
}

func usesResponses(route Route) bool {
	providerModel, _ := route.Params["model"].(string)
	provider, _, ok := strings.Cut(providerModel, "/")
	return ok && provider == "custom_openai_responses"
}

func clearReasoningItems(items []json.RawMessage) {
	for _, item := range items {
		clear(item)
	}
}

func clearRequestReasoning(request *Request) {
	if request == nil {
		return
	}
	for _, items := range request.ReasoningItems {
		clearReasoningItems(items)
	}
	request.ReasoningItems = nil
	if request.responseState != nil {
		for _, items := range request.responseState.ReasoningByCall {
			clearReasoningItems(items)
		}
		request.responseState.ReasoningByCall = nil
	}
}

func responseReasoningKey(routeID, callID string) string {
	return routeID + "\x00" + callID
}

func (s *Server) removeResponseReasoning(key string) {
	entry, ok := s.responseReasoning[key]
	if !ok {
		return
	}
	clearReasoningItems(entry.items)
	s.responseReasoningBytes -= entry.bytes
	delete(s.responseReasoning, key)
}

func (s *Server) expireResponseReasoning(now time.Time) {
	if s.responseReasoning == nil {
		s.responseReasoning = map[string]cachedResponseReasoning{}
		return
	}
	for key, entry := range s.responseReasoning {
		if !now.Before(entry.expires) {
			s.removeResponseReasoning(key)
		}
	}
}

func (s *Server) responseReasoningFor(routeID, callID string) ([]json.RawMessage, int) {
	if routeID == "" || callID == "" {
		return nil, 0
	}
	s.responseMu.Lock()
	defer s.responseMu.Unlock()
	now := time.Now()
	s.expireResponseReasoning(now)
	entry, ok := s.responseReasoning[responseReasoningKey(routeID, callID)]
	if !ok {
		return nil, 0
	}
	items := make([]json.RawMessage, len(entry.items))
	for index, item := range entry.items {
		items[index] = append(json.RawMessage(nil), item...)
	}
	return items, entry.bytes
}

func (s *Server) loadResponseReasoning(route Route, request *Request) error {
	if request == nil || !usesResponses(route) {
		return nil
	}
	loadedBytes := 0
	request.ReasoningItems = map[string][]json.RawMessage{}
	for _, message := range request.Messages {
		content, err := blocks(message.Content)
		if err != nil {
			return errProtocol
		}
		for _, block := range content {
			if block.Type != "tool_result" {
				continue
			}
			items, size := s.responseReasoningFor(route.ID, block.ToolID)
			if loadedBytes+size > responseReasoningMaxReplay {
				clearReasoningItems(items)
				return errProtocol
			}
			if len(items) > 0 {
				request.ReasoningItems[block.ToolID] = items
				loadedBytes += size
			}
		}
	}
	return nil
}

func (s *Server) storeResponseReasoning(route Route, state *responseState) {
	if !usesResponses(route) || route.ID == "" || state == nil || len(state.ReasoningByCall) == 0 {
		return
	}
	s.responseMu.Lock()
	defer s.responseMu.Unlock()
	now := time.Now()
	s.expireResponseReasoning(now)
	for callID, items := range state.ReasoningByCall {
		if callID == "" || len(items) == 0 {
			continue
		}
		entry := cachedResponseReasoning{expires: now.Add(responseReasoningTTL)}
		valid := true
		for _, item := range items {
			if len(item) == 0 || entry.bytes+len(item) > responseReasoningMaxEntry {
				valid = false
				break
			}
			entry.items = append(entry.items, append(json.RawMessage(nil), item...))
			entry.bytes += len(item)
		}
		if !valid {
			clearReasoningItems(entry.items)
			continue
		}
		key := responseReasoningKey(route.ID, callID)
		s.removeResponseReasoning(key)
		for len(s.responseReasoning) >= responseReasoningMaxEntries || s.responseReasoningBytes+entry.bytes > responseReasoningMaxBytes {
			oldestKey := ""
			var oldest time.Time
			for candidate, cached := range s.responseReasoning {
				if oldestKey == "" || cached.expires.Before(oldest) {
					oldestKey, oldest = candidate, cached.expires
				}
			}
			if oldestKey == "" {
				break
			}
			s.removeResponseReasoning(oldestKey)
		}
		if entry.bytes <= responseReasoningMaxBytes {
			s.responseReasoning[key] = entry
			s.responseReasoningBytes += entry.bytes
		}
	}
}
