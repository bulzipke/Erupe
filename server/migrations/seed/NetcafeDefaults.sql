BEGIN;

TRUNCATE public.cafebonus;

INSERT INTO public.cafebonus (time_req, item_type, item_id, quantity)
VALUES
    (1800, 17, 0, 125),
    (3600, 17, 0, 250),
    (7200, 17, 0, 500),
    (10800, 17, 0, 750),
    (18000, 17, 0, 875),
    (28800, 17, 0, 1250),
    (43200, 17, 0, 1250);

END;