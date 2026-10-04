-- Net cafe duration bonuses reset daily and are granted automatically.
-- Scale the stock 2,000 N point table to 5,000 per day; customized rows are kept.
UPDATE cafebonus AS c
SET quantity = v.new_quantity
FROM (VALUES
    (1800, 50, 125),
    (3600, 100, 250),
    (7200, 200, 500),
    (10800, 300, 750),
    (18000, 350, 875),
    (28800, 500, 1250),
    (43200, 500, 1250)
) AS v(time_req, old_quantity, new_quantity)
WHERE c.item_type = 17 AND c.time_req = v.time_req AND c.quantity = v.old_quantity;
