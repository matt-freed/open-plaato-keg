-- Demo data: six kegs on eight taps, with 30 days of history. Data only; the schema comes from
-- internal/store/schema.sql. Load into a fresh database with:
--   cat internal/store/schema.sql testdata/demo.sql | sqlite3 data/demo.db
BEGIN;
INSERT INTO kegs(id,amount_left,percent_of_beer_left,is_pouring,keg_temperature,last_pour,last_pour_string,temperature_offset,temperature_correction,weight_raw,volume_raw,pour_volume_raw,empty_keg_weight,max_keg_volume,min_temperature,max_temperature,min_temperature_max,max_temperature_min,unit,measure_unit,keg_mode,sensitivity,weight_unit,beer_left_unit_device,volume_unit,temperature_unit,keg_temperature_string,chip_temperature_string,calculated_abv,calculated_alcohol_string,wifi_signal_strength,leak_detection,firmware_version,device_og,device_fg,device_beer_style,device_date,label,display_mode,sort_order,co2_capacity,internal,extra,first_seen,last_seen,barhelper_last_sent) VALUES('00000000000000000000000000000001',0.04000000000000000083,0.0,0,22.25,0.1839999999999999969,'0.18L',-7.5,-44.0,200621.0,0.0,0.0,0.0,20.05900000000000105,0.0,30.0,40.0,-20.0,1,1,NULL,NULL,'kg','kg','litre','°C','22.25°C','74.44°C',NULL,NULL,52,0,'2.0.10a',1000.0,1000.0,NULL,NULL,'Garage Left','weight_primary',0,NULL,'{"buff-in":"1024","build":"Jul 20 2020 12:31:35","dev":"ESP32","fw":"2.0.10a","h-beat":"20","tmpl":"TMPL57889","ver":"2.0.10a"}','{}',1790634824,1790636343,1790636343);
INSERT INTO kegs(id,amount_left,percent_of_beer_left,is_pouring,keg_temperature,last_pour,last_pour_string,temperature_offset,temperature_correction,weight_raw,volume_raw,pour_volume_raw,empty_keg_weight,max_keg_volume,min_temperature,max_temperature,min_temperature_max,max_temperature_min,unit,measure_unit,keg_mode,sensitivity,weight_unit,beer_left_unit_device,volume_unit,temperature_unit,keg_temperature_string,chip_temperature_string,calculated_abv,calculated_alcohol_string,wifi_signal_strength,leak_detection,firmware_version,device_og,device_fg,device_beer_style,device_date,label,display_mode,sort_order,co2_capacity,internal,extra,first_seen,last_seen,barhelper_last_sent) VALUES('00000000000000000000000000000002',12.09999999999999965,64.0,0,3.399999999999999912,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,1,2,1,NULL,NULL,NULL,NULL,'°C',NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,'Garage Right','weight_primary',0,NULL,'{}','{}',0,0,0);
INSERT INTO kegs(id,amount_left,percent_of_beer_left,is_pouring,keg_temperature,last_pour,last_pour_string,temperature_offset,temperature_correction,weight_raw,volume_raw,pour_volume_raw,empty_keg_weight,max_keg_volume,min_temperature,max_temperature,min_temperature_max,max_temperature_min,unit,measure_unit,keg_mode,sensitivity,weight_unit,beer_left_unit_device,volume_unit,temperature_unit,keg_temperature_string,chip_temperature_string,calculated_abv,calculated_alcohol_string,wifi_signal_strength,leak_detection,firmware_version,device_og,device_fg,device_beer_style,device_date,label,display_mode,sort_order,co2_capacity,internal,extra,first_seen,last_seen,barhelper_last_sent) VALUES('00000000000000000000000000000003',9.099999999999999645,48.0,1,3.899999999999999912,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,1,2,1,NULL,NULL,NULL,NULL,'°C',NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,'Kegerator 1','weight_primary',0,NULL,'{}','{}',1790722117,1790722117,1790722117);
INSERT INTO kegs(id,amount_left,percent_of_beer_left,is_pouring,keg_temperature,last_pour,last_pour_string,temperature_offset,temperature_correction,weight_raw,volume_raw,pour_volume_raw,empty_keg_weight,max_keg_volume,min_temperature,max_temperature,min_temperature_max,max_temperature_min,unit,measure_unit,keg_mode,sensitivity,weight_unit,beer_left_unit_device,volume_unit,temperature_unit,keg_temperature_string,chip_temperature_string,calculated_abv,calculated_alcohol_string,wifi_signal_strength,leak_detection,firmware_version,device_og,device_fg,device_beer_style,device_date,label,display_mode,sort_order,co2_capacity,internal,extra,first_seen,last_seen,barhelper_last_sent) VALUES('00000000000000000000000000000004',2.200000000000000178,11.59999999999999965,0,4.099999999999999644,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,1,2,1,NULL,NULL,NULL,NULL,'°C',NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,'Kegerator 2','weight_primary',0,NULL,'{}','{}',0,0,0);
INSERT INTO kegs(id,amount_left,percent_of_beer_left,is_pouring,keg_temperature,last_pour,last_pour_string,temperature_offset,temperature_correction,weight_raw,volume_raw,pour_volume_raw,empty_keg_weight,max_keg_volume,min_temperature,max_temperature,min_temperature_max,max_temperature_min,unit,measure_unit,keg_mode,sensitivity,weight_unit,beer_left_unit_device,volume_unit,temperature_unit,keg_temperature_string,chip_temperature_string,calculated_abv,calculated_alcohol_string,wifi_signal_strength,leak_detection,firmware_version,device_og,device_fg,device_beer_style,device_date,label,display_mode,sort_order,co2_capacity,internal,extra,first_seen,last_seen,barhelper_last_sent) VALUES('00000000000000000000000000000005',11.5,61.0,NULL,2.799999999999999823,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,1,2,1,NULL,NULL,NULL,NULL,'°C',NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,'Kegerator 3','weight_primary',0,NULL,'{}','{}',0,0,0);
INSERT INTO kegs(id,amount_left,percent_of_beer_left,is_pouring,keg_temperature,last_pour,last_pour_string,temperature_offset,temperature_correction,weight_raw,volume_raw,pour_volume_raw,empty_keg_weight,max_keg_volume,min_temperature,max_temperature,min_temperature_max,max_temperature_min,unit,measure_unit,keg_mode,sensitivity,weight_unit,beer_left_unit_device,volume_unit,temperature_unit,keg_temperature_string,chip_temperature_string,calculated_abv,calculated_alcohol_string,wifi_signal_strength,leak_detection,firmware_version,device_og,device_fg,device_beer_style,device_date,label,display_mode,sort_order,co2_capacity,internal,extra,first_seen,last_seen,barhelper_last_sent) VALUES('00000000000000000000000000000006',15.19999999999999929,80.0,NULL,3.100000000000000088,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,1,2,1,NULL,NULL,NULL,NULL,'°C',NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL,'Kegerator 4','weight_primary',0,NULL,'{}','{}',0,0,0);
-- History: 30 days of readings for every keg, one every 5 minutes, ending at
-- the moment this file is loaded, so every range on the History page has
-- data however long ago the demo was seeded. Generated rather than listed,
-- and from plain arithmetic rather than random(), so every load gives the same
-- history shifted to that day. Each keg's newest row is its current reading.
--
-- Pours land at a few a day, 0.35-0.75 in the keg's own unit. Going back in
-- time the amount rises by every pour since; past a full keg it wraps, so each
-- keg drains, is swapped for a full one, and drains again. Temperature swings
-- over the day with a shorter fridge cycle on top. Kegerator 3 is offline for
-- about five hours six days back.
WITH RECURSIVE
  steps(n) AS (SELECT 0 UNION ALL SELECT n + 1 FROM steps WHERE n < 8640),
  -- id, pattern seed, current amount, full keg in the same unit, current
  -- temperature, current percent
  now_(id, seed, amount, full, temp, pct) AS (VALUES
    ('00000000000000000000000000000001', 1, 0.04, 20.059, 22.25, 0.0),
    ('00000000000000000000000000000002', 2, 12.1, 18.906, 3.4,  64.0),
    ('00000000000000000000000000000003', 3, 9.1,  18.958, 3.9,  48.0),
    ('00000000000000000000000000000004', 4, 2.2,  18.966, 4.1,  11.6),
    ('00000000000000000000000000000005', 5, 11.5, 18.852, 2.8,  61.0),
    ('00000000000000000000000000000006', 6, 15.2, 19.0,   3.1,  80.0)),
  pours AS (
    SELECT k.*, s.n,
      CASE WHEN (s.n * 7919 + k.seed * 104729) % 97 = 0
           THEN 0.35 + ((s.n * 31 + k.seed * 17) % 40) / 100.0 ELSE 0 END AS poured
    FROM now_ k CROSS JOIN steps s),
  readings AS (
    SELECT *,
      -- Everything poured after this reading was taken.
      COALESCE(SUM(poured) OVER (PARTITION BY id ORDER BY n
        ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING), 0) AS since
    FROM pours),
  shaped AS (
    SELECT id, n, poured, full, temp, pct,
      (amount + since) - full * CAST((amount + since) / full AS INTEGER) AS left_
    FROM readings)
INSERT INTO keg_log(keg_id,ts,amount_left,keg_temperature,percent_of_beer_left,is_pouring)
SELECT id,
  CAST(strftime('%s','now') AS INTEGER) - n * 300,
  ROUND(left_, 3),
  -- Triangle waves, both zero at n = 0 so the newest row is the current temperature.
  ROUND(temp + 1.2 * (ABS((n % 288) * 2.0 / 288 - 1) - 1) + 0.15 * (ABS((n % 9) * 2.0 / 9 - 1) - 1), 2),
  -- The newest row takes the scale's own percent, which can differ from
  -- amount over full by rounding.
  CASE WHEN n = 0 THEN pct ELSE ROUND(left_ / full * 100, 1) END,
  poured > 0
FROM shaped
WHERE NOT (id = '00000000000000000000000000000005' AND n BETWEEN 1700 AND 1760);
INSERT INTO taps(id,tap_number,name,brewery,style,abv,ibu,color,description,tasting_notes,keg_id,srm,color_preset,kegged_date) VALUES('0f311b5d',3,'Hazy Daze IPA','','New England IPA',6.799999999999999823,45.0,'#e8b33a','Juicy and soft, double dry hopped with Citra, Mosaic and Galaxy for waves of tropical fruit, stone fruit and citrus, balanced by a pillowy oat body and a gentle, lingering bitterness.','','00000000000000000000000000000002',5.0,'','2026-09-03');
INSERT INTO taps(id,tap_number,name,brewery,style,abv,ibu,color,description,tasting_notes,keg_id,srm,color_preset,kegged_date) VALUES('54a13e86',1,'Midnight Oil','','Oatmeal Stout',5.900000000000000355,32.0,'#3b2314','Silky oatmeal stout with roasted coffee, dark chocolate and a creamy finish.','','00000000000000000000000000000003',65.0,'','2026-09-01');
INSERT INTO taps(id,tap_number,name,brewery,style,abv,ibu,color,description,tasting_notes,keg_id,srm,color_preset,kegged_date) VALUES('649bb4b4',2,'Backyard Pils','','German Pilsner',4.900000000000000355,38.0,'#f1d56a','','','00000000000000000000000000000004',3.0,'','2026-09-29');
INSERT INTO taps(id,tap_number,name,brewery,style,abv,ibu,color,description,tasting_notes,keg_id,srm,color_preset,kegged_date) VALUES('6cd13cb4',5,'Sparkling Water','','Carbonated Water',NULL,NULL,'#9fd3e6','Filtered and lightly carbonated.','','00000000000000000000000000000005',NULL,'clear','');
INSERT INTO taps(id,tap_number,name,brewery,style,abv,ibu,color,description,tasting_notes,keg_id,srm,color_preset,kegged_date) VALUES('6ec0e1cf',7,'Backyard Rosé Cider','','Rosé Cider',6.0,NULL,'#f3d98a','','','00000000000000000000000000000006',NULL,'pink','2026-09-10');
INSERT INTO taps(id,tap_number,name,brewery,style,abv,ibu,color,description,tasting_notes,keg_id,srm,color_preset,kegged_date) VALUES('a366cdd8',4,'Red Barn Amber','','American Amber Ale',5.400000000000000355,NULL,'#b5501f','','','00000000000000000000000000000001',15.0,'','2026-07-20');
INSERT INTO taps(id,tap_number,name,brewery,style,abv,ibu,color,description,tasting_notes,keg_id,srm,color_preset,kegged_date) VALUES('a5106a35',6,'Winter Warmer','','Old Ale',8.199999999999999289,40.0,'#7a2e12','','','',22.0,'','2026-09-15');
INSERT INTO taps(id,tap_number,name,brewery,style,abv,ibu,color,description,tasting_notes,keg_id,srm,color_preset,kegged_date) VALUES('efa282b5',8,'Cold Brew Porter','','Coffee Porter',6.099999999999999645,30.0,'#2a170c','','','',32.0,'','');
INSERT INTO app_config(key,value) VALUES('display_unit_system','us');
COMMIT;
