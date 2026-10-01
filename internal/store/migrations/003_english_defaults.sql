-- English default labels for the built-in categories (custom labels are kept).
UPDATE anomaly_rules SET label = 'Trash' WHERE category = 'trash' AND label = 'Sampah';
UPDATE anomaly_rules SET label = 'Spill' WHERE category = 'spill' AND label = 'Tumpahan';
UPDATE anomaly_rules SET label = 'Floor damage' WHERE category = 'floor_damage' AND label = 'Lantai Rusak';
UPDATE anomaly_rules SET label = 'Fallen person' WHERE category = 'fallen_person' AND label = 'Orang Terjatuh';
UPDATE anomaly_rules SET label = 'Other' WHERE category = 'unknown' AND label = 'Lainnya';
