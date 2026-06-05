package memory

import (
	"context"
	"sync"

	"github.com/akshayvadher/test-n-design-go/internal/membership"
)

// Repository is the in-memory membership.Repository implementation. It is
// safe for concurrent use. Members are stored by MemberId;
// FindMemberByEmail is a linear scan because the source TS in-memory
// repository does the same and the test substrate never holds enough
// rows for the scan to matter.
type Repository struct {
	mu          sync.RWMutex
	membersById map[membership.MemberId]membership.MemberDto
}

// Compile-time assertion that *Repository satisfies the membership
// driven port.
var _ membership.Repository = (*Repository)(nil)

// NewRepository constructs an empty in-memory Repository.
func NewRepository() *Repository {
	return &Repository{
		membersById: map[membership.MemberId]membership.MemberDto{},
	}
}

// SaveMember upserts the member by MemberId.
func (r *Repository) SaveMember(_ context.Context, member membership.MemberDto) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.membersById[member.MemberId] = member
	return nil
}

// FindMemberById returns the stored member by value, or (nil, nil) on miss.
func (r *Repository) FindMemberById(_ context.Context, memberId membership.MemberId) (*membership.MemberDto, error) {
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
func (r *Repository) FindMemberByEmail(_ context.Context, email string) (*membership.MemberDto, error) {
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
