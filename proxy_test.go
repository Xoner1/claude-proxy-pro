package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestThinkTagParser(t *testing.T) {
	tests := []struct {
		name     string
		feeds    []string
		expected []ContentChunk
	}{
		{
			name:  "plain text",
			feeds: []string{"hello world"},
			expected: []ContentChunk{
				{Type: ContentTypeText, Content: "hello world"},
			},
		},
		{
			name:  "simple think",
			feeds: []string{"<think>reasoning</think>"},
			expected: []ContentChunk{
				{Type: ContentTypeThinking, Content: "reasoning"},
			},
		},
		{
			name:  "mixed content",
			feeds: []string{"hello <think>reasoning</think> world"},
			expected: []ContentChunk{
				{Type: ContentTypeText, Content: "hello "},
				{Type: ContentTypeThinking, Content: "reasoning"},
				{Type: ContentTypeText, Content: " world"},
			},
		},
		{
			name:  "split think tags",
			feeds: []string{"hello <thi", "nk>reasoning</th", "ink> world"},
			expected: []ContentChunk{
				{Type: ContentTypeText, Content: "hello "},
				{Type: ContentTypeThinking, Content: "reasoning"},
				{Type: ContentTypeText, Content: " world"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewThinkTagParser()
			var chunks []ContentChunk
			for _, feed := range tt.feeds {
				chunks = append(chunks, p.Feed(feed)...)
			}
			if remaining := p.Flush(); remaining != nil {
				chunks = append(chunks, *remaining)
			}

			if !reflect.DeepEqual(chunks, tt.expected) {
				t.Errorf("expected %v, got %v", tt.expected, chunks)
			}
		})
	}
}

func TestHeuristicToolParser(t *testing.T) {
	tests := []struct {
		name          string
		feeds         []string
		expectedText  string
		expectedTools []string // names of tools
	}{
		{
			name:          "plain text",
			feeds:         []string{"hello world"},
			expectedText:  "hello world",
			expectedTools: nil,
		},
		{
			name:          "standard function parameter form",
			feeds:         []string{"● <function=WebSearch><parameter=query>golang proxy</parameter>"},
			expectedText:  "",
			expectedTools: []string{"WebSearch"},
		},
		{
			name:          "web search json format",
			feeds:         []string{"use WebSearch {\"query\": \"deep learning\"}"},
			expectedText:  "",
			expectedTools: []string{"WebSearch"},
		},
		{
			name:          "web fetch json format",
			feeds:         []string{"use WebFetch {\"url\": \"https://go.dev\"}"},
			expectedText:  "",
			expectedTools: []string{"WebFetch"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewHeuristicToolParser()
			var text string
			var tools []map[string]interface{}

			for _, feed := range tt.feeds {
				txt, tls := p.Feed(feed)
				text += txt
				tools = append(tools, tls...)
			}
			tools = append(tools, p.Flush()...)

			if text != tt.expectedText {
				t.Errorf("expected text %q, got %q", tt.expectedText, text)
			}

			var toolNames []string
			for _, tl := range tools {
				if name, ok := tl["name"].(string); ok {
					toolNames = append(toolNames, name)
				}
			}

			if !reflect.DeepEqual(toolNames, tt.expectedTools) {
				t.Errorf("expected tools %v, got %v", tt.expectedTools, toolNames)
			}
		})
	}
}

func TestTranslateMessagesDeferred(t *testing.T) {
	// Assistant message has tool_use at block index 1, and trailing text at block index 2
	assistantMsg := AnthropicMsg{
		Role: "assistant",
		Content: json.RawMessage(`[
			{"type": "text", "text": "I will search now."},
			{"type": "tool_use", "id": "call_1", "name": "WebSearch", "input": {"query": "go test"}},
			{"type": "text", "text": "Searching is done."}
		]`),
	}

	// User response has tool_result
	userMsg := AnthropicMsg{
		Role: "user",
		Content: json.RawMessage(`[
			{"type": "tool_result", "tool_use_id": "call_1", "content": "Search results for go test..."}
		]`),
	}

	msgs := []AnthropicMsg{assistantMsg, userMsg}
	translated := translateMessages(msgs)

	// We expect:
	// 1. Assistant message with "I will search now." and tool call "call_1"
	// 2. Tool result message for "call_1" with "Search results..."
	// 3. Deferred Assistant message with "Searching is done."

	if len(translated) != 3 {
		t.Fatalf("expected 3 translated messages, got %d", len(translated))
	}

	if translated[0].Role != "assistant" || len(translated[0].ToolCalls) != 1 {
		t.Errorf("unexpected first message: %+v", translated[0])
	}
	if translated[1].Role != "tool" || translated[1].ToolCallID != "call_1" {
		t.Errorf("unexpected second message: %+v", translated[1])
	}
	if translated[2].Role != "assistant" || translated[2].Content != "Searching is done." {
		t.Errorf("unexpected third message: %+v", translated[2])
	}
}
