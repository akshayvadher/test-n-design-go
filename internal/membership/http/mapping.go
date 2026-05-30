package http

import (
	"github.com/akshayvadher/test-n-design-go/internal/membership"
)

// fromRegisterMemberRequest translates the inbound HTTP DTO into the
// membership domain DTO. The translation is field-for-field; validation
// lives inside the facade.
func fromRegisterMemberRequest(req RegisterMemberRequest) membership.NewMemberDto {
	return membership.NewMemberDto{
		Name:  req.Name,
		Email: req.Email,
	}
}

// toMemberResponse translates a membership.MemberDto into the outbound
// HTTP DTO. String casts unwrap the MemberId / MembershipTier /
// MembershipStatus named types so JSON encoding sees plain strings.
func toMemberResponse(member membership.MemberDto) MemberResponse {
	return MemberResponse{
		MemberId: string(member.MemberId),
		Name:     member.Name,
		Email:    member.Email,
		Tier:     string(member.Tier),
		Status:   string(member.Status),
	}
}

// toEligibilityResponse translates a membership.EligibilityDto into the
// outbound HTTP DTO.
func toEligibilityResponse(eligibility membership.EligibilityDto) EligibilityResponse {
	return EligibilityResponse{
		MemberId: string(eligibility.MemberId),
		Eligible: eligibility.Eligible,
		Reason:   eligibility.Reason,
	}
}
