-- Existing rounds retain every original phase/reward deadline. Only newly
-- created normal rounds opt in to the continuous seven-day phase schedule.
ALTER TABLE events ADD COLUMN diva_continuous boolean NOT NULL DEFAULT false;
