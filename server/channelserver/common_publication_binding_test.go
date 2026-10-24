package channelserver

import (
	"erupe-ce/common/questdelivery"
	"testing"
)

func TestCommonPublicationServerRequiresSamePreparedCatalogLocation(t *testing.T) {
	catalog, err := questdelivery.Parse([]byte(`{"version":"server-binding","profiles":[{"id":"p","worlds":["One"]}],"schedules":[{"id":"always"}],"quests":[{"quest_id":1,"profiles":["p"],"schedule":"always"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	publication, err := questdelivery.BuildCommonPublicationCohort(catalog, []questdelivery.PublicationLocation{{World: "One", Channel: 1}})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{questDelivery: catalog, commonPublication: publication, worldName: "One", channelNumber: 1}
	if err := s.validateCommonPublicationBinding(); err != nil {
		t.Fatal(err)
	}
	s.channelNumber = 2
	if err := s.validateCommonPublicationBinding(); err == nil {
		t.Fatal("unconfigured server location accepted")
	}
	s.channelNumber = 1
	s.commonPublication = nil
	if err := s.validateCommonPublicationBinding(); err == nil {
		t.Fatal("missing publication proof accepted")
	}
}
