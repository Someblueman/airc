package main

import (
	"encoding/json"
	"errors"
	"strconv"
)

// Budget the exact JSON bytes, including the trailing newline. Encode the full
// result once, then subtract each removed record's encoding and array comma.
// Omission counters are always present in the wire object; account for changes
// in their decimal width too. Text and protected records are never truncated.
func budgetContext(r *conversationContext, protected map[string]bool, budget int) ([]byte, error) {
	data, err := json.Marshal(r)
	if err != nil || len(data)+1 <= budget {
		return data, err
	}
	size := len(data) + 1
	remove := func(record any, count int, omitted *int) error {
		encoded, err := json.Marshal(record)
		if err != nil {
			return err
		}
		size -= len(encoded)
		if count > 1 {
			size-- // one array separator disappears with the element
		}
		size += len(strconv.Itoa(*omitted+1)) - len(strconv.Itoa(*omitted))
		*omitted++
		return nil
	}
	for size > budget && len(r.Participants) > 0 {
		n := len(r.Participants)
		if err := remove(r.Participants[n-1], n, &r.OmittedProfiles); err != nil {
			return nil, err
		}
		r.Participants = r.Participants[:n-1]
	}
	for size > budget && len(r.Pins) > 0 {
		n := len(r.Pins)
		if err := remove(r.Pins[n-1], n, &r.OmittedPins); err != nil {
			return nil, err
		}
		r.Pins = r.Pins[:n-1]
	}
	kept, remaining := 0, len(r.Messages)
	for _, m := range r.Messages {
		if size > budget && !protected[m.ID] {
			if err := remove(m, remaining, &r.OmittedMessages); err != nil {
				return nil, err
			}
			remaining--
			continue
		}
		r.Messages[kept] = m
		kept++
	}
	clear(r.Messages[kept:])
	r.Messages = r.Messages[:kept]
	if size > budget {
		return nil, errors.New("trigger/root/correction context exceeds max-bytes; increase the budget")
	}
	return json.Marshal(r)
}
