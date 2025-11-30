--
-- Migration to add hourly stats tables
-- These tables mirror the daily stats tables but use datetime instead of date
-- to track hourly statistics
--

-- Hourly pokemon stats (total pokemon encounters per hour)
CREATE TABLE IF NOT EXISTS `pokemon_stats_hourly` (
  `datetime` datetime NOT NULL,
  `area` varchar(255) NOT NULL DEFAULT '',
  `fence` varchar(255) NOT NULL DEFAULT '',
  `pokemon_id` smallint unsigned NOT NULL,
  `form_id` smallint unsigned NOT NULL,
  `count` int NOT NULL,
  PRIMARY KEY (`datetime`, `area`, `fence`, `pokemon_id`, `form_id`) USING BTREE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Hourly pokemon IV stats (pokemon with IV data per hour)
CREATE TABLE IF NOT EXISTS `pokemon_iv_stats_hourly` (
  `datetime` datetime NOT NULL,
  `area` varchar(255) NOT NULL DEFAULT '',
  `fence` varchar(255) NOT NULL DEFAULT '',
  `pokemon_id` smallint unsigned NOT NULL,
  `form_id` smallint unsigned NOT NULL,
  `count` int NOT NULL,
  PRIMARY KEY (`datetime`, `area`, `fence`, `pokemon_id`, `form_id`) USING BTREE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Hourly pokemon hundo stats (perfect IV pokemon per hour)
CREATE TABLE IF NOT EXISTS `pokemon_hundo_stats_hourly` (
  `datetime` datetime NOT NULL,
  `area` varchar(255) NOT NULL DEFAULT '',
  `fence` varchar(255) NOT NULL DEFAULT '',
  `pokemon_id` smallint unsigned NOT NULL,
  `form_id` smallint unsigned NOT NULL,
  `count` int NOT NULL,
  PRIMARY KEY (`datetime`, `area`, `fence`, `pokemon_id`, `form_id`) USING BTREE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Hourly pokemon nundo stats (zero IV pokemon per hour)
CREATE TABLE IF NOT EXISTS `pokemon_nundo_stats_hourly` (
  `datetime` datetime NOT NULL,
  `area` varchar(255) NOT NULL DEFAULT '',
  `fence` varchar(255) NOT NULL DEFAULT '',
  `pokemon_id` smallint unsigned NOT NULL,
  `form_id` smallint unsigned NOT NULL,
  `count` int NOT NULL,
  PRIMARY KEY (`datetime`, `area`, `fence`, `pokemon_id`, `form_id`) USING BTREE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Hourly pokemon shiny stats (shiny encounters per hour)
CREATE TABLE IF NOT EXISTS `pokemon_shiny_stats_hourly` (
  `datetime` datetime NOT NULL,
  `area` varchar(255) NOT NULL DEFAULT '',
  `fence` varchar(255) NOT NULL DEFAULT '',
  `pokemon_id` smallint unsigned NOT NULL,
  `form_id` smallint unsigned NOT NULL,
  `count` int NOT NULL,
  `total` int NOT NULL DEFAULT 0,
  PRIMARY KEY (`datetime`, `area`, `fence`, `pokemon_id`, `form_id`) USING BTREE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Hourly raid stats
CREATE TABLE IF NOT EXISTS `raid_stats_hourly` (
  `datetime` datetime NOT NULL,
  `area` varchar(255) NOT NULL DEFAULT '',
  `fence` varchar(255) NOT NULL DEFAULT '',
  `pokemon_id` smallint unsigned NOT NULL,
  `form_id` smallint unsigned NOT NULL,
  `level` smallint unsigned,
  `count` int NOT NULL,
  PRIMARY KEY (`datetime`, `area`, `fence`, `pokemon_id`, `form_id`, `level`) USING BTREE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Hourly invasion stats
CREATE TABLE IF NOT EXISTS `invasion_stats_hourly` (
  `datetime` datetime NOT NULL,
  `area` varchar(255) NOT NULL DEFAULT '',
  `fence` varchar(255) NOT NULL DEFAULT '',
  `character` smallint unsigned NOT NULL,
  `count` int NOT NULL,
  PRIMARY KEY (`datetime`, `area`, `fence`, `character`) USING BTREE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;

-- Hourly quest stats
CREATE TABLE IF NOT EXISTS `quest_stats_hourly` (
  `datetime` datetime NOT NULL,
  `area` varchar(255) NOT NULL DEFAULT '',
  `fence` varchar(255) NOT NULL DEFAULT '',
  `reward_type` smallint unsigned NOT NULL,
  `pokemon_id` smallint unsigned NOT NULL DEFAULT 0,
  `item_id` smallint unsigned NOT NULL DEFAULT 0,
  `item_amount` smallint unsigned NOT NULL,
  `count` int NOT NULL,
  PRIMARY KEY (`datetime`, `area`, `fence`, `reward_type`, `pokemon_id`, `item_id`, `item_amount`) USING BTREE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci;
