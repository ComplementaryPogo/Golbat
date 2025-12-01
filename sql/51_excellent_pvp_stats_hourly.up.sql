--
-- Migration to add hourly excellent PVP stats table
-- Tracks pokemon that rank within the configured threshold for each PVP league
--

CREATE TABLE IF NOT EXISTS `pokemon_excellent_pvp_stats_hourly` (
  `datetime` datetime NOT NULL,
  `area` varchar(255) NOT NULL DEFAULT '',
  `fence` varchar(255) NOT NULL DEFAULT '',
  `pokemon_id` smallint unsigned NOT NULL,
  `form_id` smallint unsigned NOT NULL,
  `league` varchar(32) NOT NULL,
  `count` int NOT NULL,
  PRIMARY KEY (`datetime`, `area`, `fence`, `pokemon_id`, `form_id`, `league`) USING BTREE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;
