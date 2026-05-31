package lending

// cloneReservationDto returns a defensive copy of reservation so internal
// state and returned values do not share the FulfilledAt pointer. The
// auto-loan saga uses it when staging the fulfilled / un-fulfilled
// variants so the staged dto is independent of the source value.
func cloneReservationDto(reservation ReservationDto) ReservationDto {
	clone := reservation
	if reservation.FulfilledAt != nil {
		fulfilledAt := *reservation.FulfilledAt
		clone.FulfilledAt = &fulfilledAt
	}
	return clone
}
