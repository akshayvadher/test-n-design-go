package membership

import (
	"context"
	"sync"
)

// InMemoryRepository is the in-memory Repository implementation. It is
// safe for concurrent use. Members are stored by MemberId; FindMemberByEmail
// is a linear scan because the source TS in-memory repository does the
// same and the test substrate never holds enough rows for the scan to
// matter.
type InMemoryRepository struct {
	mu          sync.RWMutex
	membersById map[MemberId]MemberDto
}

// NewInMemoryRepository constructs an empty InMemoryRepository.
func NewInMemoryRepository() *InMemoryRepository {
	return &InMemoryRepository{
		membersById: map[MemberId]MemberDto{},
	}
}

// SaveMember upserts the member by MemberId.
func (r *InMemoryRepository) SaveMember(_ context.Context, member MemberDto) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.membersById[member.MemberId] = member
	return nil
}

// FindMemberById returns the stored member by value, or (nil, nil) on miss.
func (r *InMemoryRepository) FindMemberById(_ context.Context, memberId MemberId) (*MemberDto, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	member, ok := r.membersById[memberId]
	if !ok {
		return nil, nil
	}
	return &member, nil
}

// FindMemberByEmail scans the members for a matching email. Returns
// (nil, nil) on miss.
func (r *InMemoryRepository) FindMemberByEmail(_ context.Context, email string) (*MemberDto, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, member := range r.membersById {
		if member.Email == email {
			copied := member
			return &copied, nil
		}
	}
	return nil, nil
}
