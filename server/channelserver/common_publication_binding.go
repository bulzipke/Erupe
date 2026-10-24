package channelserver

import "fmt"

func (s *Server) validateCommonPublicationBinding() error {
	if s.questDelivery == nil && s.commonPublication == nil {
		return nil
	}
	if !s.commonPublication.BoundTo(s.questDelivery) || !s.commonPublication.PermitsLocation(s.worldName, s.channelNumber) {
		return fmt.Errorf("common publication content/catalog/location binding is not prepared")
	}
	return nil
}
