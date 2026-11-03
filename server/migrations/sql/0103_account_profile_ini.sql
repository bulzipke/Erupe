ALTER TABLE account_keyconfigs DROP CONSTRAINT account_keyconfigs_slot_check;
ALTER TABLE account_keyconfigs ADD CONSTRAINT account_keyconfigs_slot_check CHECK(slot IN (0,1,2));
ALTER TABLE account_keyconfigs ADD CONSTRAINT account_keyconfigs_profile_size_check
    CHECK((slot IN (0,1) AND octet_length(data)<=524288) OR (slot=2 AND octet_length(data)<=131072));
