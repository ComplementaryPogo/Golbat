package decoder

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/UnownHash/gohbem"
	"github.com/jmoiron/sqlx"
	log "github.com/sirupsen/logrus"

	"golbat/config"
	"golbat/encounter_cache"
	"golbat/geo"
)

type areaStatsCount struct {
	tthBucket             [12]int
	monsSeen              int
	verifiedEnc           int
	unverifiedEnc         int
	verifiedEncSecTotal   int64
	monsIv                int
	timeToEncounterCount  int
	timeToEncounterSum    int64
	statsResetCount       int
	verifiedReEncounter   int
	verifiedReEncSecTotal int64
}

var pokemonCount = make(map[geo.AreaName]*areaPokemonCountDetail)
var raidCount = make(map[geo.AreaName]map[int64]*areaRaidCountDetail)
var invasionCount = make(map[geo.AreaName]*areaInvasionCountDetail)
var questCount = make(map[geo.AreaName]map[int]areaQuestCountDetail)

// Hourly stats maps - mirror the daily maps but flushed hourly
var pokemonCountHourly = make(map[geo.AreaName]*areaPokemonCountDetail)
var raidCountHourly = make(map[geo.AreaName]map[int64]*areaRaidCountDetail)
var invasionCountHourly = make(map[geo.AreaName]*areaInvasionCountDetail)
var questCountHourly = make(map[geo.AreaName]map[int]areaQuestCountDetail)

// max dex id
const maxPokemonNo = 1050
const maxInvasionCharacter = 523
const maxItemNo = 1614

const batchInsertSize = 200

type shinyChecks struct {
	shiny int
	total int
}

type pokemonForm struct {
	pokemonId int16
	formId    int
}

type areaPokemonCountDetail struct {
	hundos      map[pokemonForm]int
	nundos      map[pokemonForm]int
	shinyChecks map[pokemonForm]shinyChecks
	count       map[pokemonForm]int
	ivCount     map[pokemonForm]int
}

type raidPokemonKey struct {
	pokemonId int16
	formId    int
	tempEvoId int
}

type areaRaidCountDetail struct {
	count map[raidPokemonKey]int
}

type areaInvasionCountDetail struct {
	count [maxInvasionCharacter + 1]int
}

type areaQuestCountDetail struct {
	count          int
	pokemonDetails [maxPokemonNo + 1]map[int]int // for each pokemonId[megaCount] keep a count
	itemDetails    [maxItemNo + 1]map[int]int    // for each itemId[amount] keep a count
}

// a cache indexed by encounterId (Pokemon.Id)
var encounterCache *encounter_cache.EncounterCache

var pokemonStats = make(map[geo.AreaName]areaStatsCount)
var pokemonStatsLock sync.Mutex
var raidStatsLock sync.Mutex
var incidentStatsLock sync.Mutex
var questStatsLock sync.Mutex

// Hourly stats locks - separate from daily to avoid contention
var pokemonStatsHourlyLock sync.Mutex
var raidStatsHourlyLock sync.Mutex
var incidentStatsHourlyLock sync.Mutex
var questStatsHourlyLock sync.Mutex

// Excellent PVP stats (hourly only)
// Key is pokemonForm, value is map of league name to count
type areaPvpCountDetail struct {
	excellentPvp map[pokemonForm]map[string]int // pokemonForm -> league -> count
}

var excellentPvpCountHourly = make(map[geo.AreaName]*areaPvpCountDetail)
var excellentPvpStatsHourlyLock sync.Mutex

func initLiveStats() {
	encounterCache = encounter_cache.NewEncounterCache(60 * time.Minute)
	// TODO: fix later to shutdown cleanly, if we care.
	go encounterCache.Run(context.Background())
}

func LoadStatsGeofences() {
	if err := ReadGeofences(); err != nil {
		if os.IsNotExist(err) {
			log.Infof("No geofence file found, skipping")
			return
		}
		panic(fmt.Sprintf("Error reading geofences: %v", err))
	}
}

func StartStatsWriter(statsDb *sqlx.DB) {
	ticker := time.NewTicker(time.Duration(config.Config.StatsIntervals.PokemonStatsIntervalMinutes) * time.Minute)
	go func() {
		for {
			<-ticker.C
			logPokemonStats(statsDb)
		}
	}()

	t2 := time.NewTicker(time.Duration(config.Config.StatsIntervals.PokemonCountIntervalMinutes) * time.Minute)
	go func() {
		for {
			<-t2.C
			logPokemonCount(statsDb)
		}
	}()

	t4 := time.NewTicker(time.Duration(config.Config.StatsIntervals.RaidStatsIntervalMinutes) * time.Minute)
	go func() {
		for {
			<-t4.C
			logRaidStats(statsDb)
		}
	}()

	t5 := time.NewTicker(time.Duration(config.Config.StatsIntervals.InvasionStatsIntervalMinutes) * time.Minute)
	go func() {
		for {
			<-t5.C
			logInvasionStats(statsDb)
		}
	}()

	t6 := time.NewTicker(time.Duration(config.Config.StatsIntervals.QuestStatsIntervalMinutes) * time.Minute)
	go func() {
		for {
			<-t6.C
			logQuestStats(statsDb)
		}
	}()

	// Hourly stats writers - flush accumulated hourly stats to database
	hourlyTicker := time.NewTicker(1 * time.Hour)
	go func() {
		for {
			<-hourlyTicker.C
			logPokemonCountHourly(statsDb)
		}
	}()

	hourlyRaidTicker := time.NewTicker(1 * time.Hour)
	go func() {
		for {
			<-hourlyRaidTicker.C
			logRaidStatsHourly(statsDb)
		}
	}()

	hourlyInvasionTicker := time.NewTicker(1 * time.Hour)
	go func() {
		for {
			<-hourlyInvasionTicker.C
			logInvasionStatsHourly(statsDb)
		}
	}()

	hourlyQuestTicker := time.NewTicker(1 * time.Hour)
	go func() {
		for {
			<-hourlyQuestTicker.C
			logQuestStatsHourly(statsDb)
		}
	}()

	hourlyPvpTicker := time.NewTicker(1 * time.Hour)
	go func() {
		for {
			<-hourlyPvpTicker.C
			logExcellentPvpStatsHourly(statsDb)
		}
	}()
}

func ReloadGeofenceAndClearStats() {
	log.Info("Reloading stats geofence")

	pokemonStatsLock.Lock()
	pokemonStatsHourlyLock.Lock()
	excellentPvpStatsHourlyLock.Lock()
	defer pokemonStatsLock.Unlock()
	defer pokemonStatsHourlyLock.Unlock()
	defer excellentPvpStatsHourlyLock.Unlock()

	if err := ReadGeofences(); err != nil {
		log.Errorf("Error reading geofences during hot-reload: %v", err)
		return
	}
	pokemonStats = make(map[geo.AreaName]areaStatsCount)          // clear stats
	pokemonCount = make(map[geo.AreaName]*areaPokemonCountDetail) // clear count

	// Clear hourly stats as well
	pokemonCountHourly = make(map[geo.AreaName]*areaPokemonCountDetail)
	excellentPvpCountHourly = make(map[geo.AreaName]*areaPvpCountDetail)
}

// update stats for an encounterId
func updateEncounterStats(pokemon *Pokemon) {
	// We should only be called from encounters. It's important to do so,
	// so that the 'DuplicateEncounters' stats below are correct.
	// And double check that we have IVs, anyway.
	if !(pokemon.AtkIv.Valid && pokemon.DefIv.Valid && pokemon.StaIv.Valid) {
		return
	}

	// Keep track of encounter Id -> account username. Count shinies
	// for the same encounter Ids, but only if an account has not seen
	// it before. We'll ignore things like re-rolls.

	username := pokemon.Username.ValueOrZero()
	if username == "" {
		username = "<NoUsername>"
	}

	encounterCacheVal := encounterCache.GetOrCreate(uint64(pokemon.Id))
	isNewEncounter := encounterCacheVal.NumAccountsSeen() == 0

	if encounterCacheVal.SetAccountSeen(pokemon.Username.ValueOrZero()) {
		// account has already seen this encounter Id
		statsCollector.IncDuplicateEncounters(true)
		return
	}

	if !isNewEncounter {
		// at least one other account has already seen this
		// encounter. This is the first time for this account.
		statsCollector.IncDuplicateEncounters(false)
	}

	encounterCache.Put(uint64(pokemon.Id), encounterCacheVal, pokemon.remainingDuration(time.Now().Unix()))

	pokemonIdStr := strconv.Itoa(int(pokemon.PokemonId))
	var formId int
	var formIdStr string
	if pokemon.Form.Valid {
		formId = int(pokemon.Form.ValueOrZero())
		formIdStr = strconv.Itoa(int(pokemon.Form.ValueOrZero()))
	}

	// For the DB (daily stats)
	func() {
		areaName := geo.AreaName{Parent: "world", Name: "world"}

		pokemonStatsLock.Lock()
		defer pokemonStatsLock.Unlock()

		countStats, exists := pokemonCount[areaName]
		if !exists {
			countStats = &areaPokemonCountDetail{
				hundos:      make(map[pokemonForm]int),
				nundos:      make(map[pokemonForm]int),
				count:       make(map[pokemonForm]int),
				ivCount:     make(map[pokemonForm]int),
				shinyChecks: make(map[pokemonForm]shinyChecks),
			}
			pokemonCount[areaName] = countStats
		}

		pf := pokemonForm{pokemonId: pokemon.PokemonId, formId: formId}

		entry := countStats.shinyChecks[pf]
		entry.total++
		if pokemon.Shiny.ValueOrZero() {
			entry.shiny++
		}
		countStats.shinyChecks[pf] = entry
	}()

	// For the DB (hourly stats)
	func() {
		areaName := geo.AreaName{Parent: "world", Name: "world"}

		pokemonStatsHourlyLock.Lock()
		defer pokemonStatsHourlyLock.Unlock()

		countStats, exists := pokemonCountHourly[areaName]
		if !exists {
			countStats = &areaPokemonCountDetail{
				hundos:      make(map[pokemonForm]int),
				nundos:      make(map[pokemonForm]int),
				count:       make(map[pokemonForm]int),
				ivCount:     make(map[pokemonForm]int),
				shinyChecks: make(map[pokemonForm]shinyChecks),
			}
			pokemonCountHourly[areaName] = countStats
		}

		pf := pokemonForm{pokemonId: pokemon.PokemonId, formId: formId}

		entry := countStats.shinyChecks[pf]
		entry.total++
		if pokemon.Shiny.ValueOrZero() {
			entry.shiny++
		}
		countStats.shinyChecks[pf] = entry
	}()

	// Prometheus
	if pokemon.Shiny.ValueOrZero() {
		statsCollector.IncPokemonCountShiny(pokemonIdStr, formIdStr)
		if pokemon.AtkIv.Int64 == 15 && pokemon.DefIv.Int64 == 15 && pokemon.StaIv.Int64 == 15 {
			statsCollector.IncPokemonCountShundo()
		} else if pokemon.AtkIv.Int64 == 0 && pokemon.DefIv.Int64 == 0 && pokemon.StaIv.Int64 == 0 {
			statsCollector.IncPokemonCountSnundo()
		}
	} else {
		// send non-shinies also, so that we can compute odds.
		statsCollector.IncPokemonCountNonShiny(pokemonIdStr, formIdStr)
	}
}

func updatePokemonStats(pokemon *Pokemon, areas []geo.AreaName, now int64, pvpResults map[string][]gohbem.PokemonEntry) {
	if len(areas) == 0 {
		areas = []geo.AreaName{
			{
				Parent: "unmatched",
				Name:   "unmatched",
			},
		}
	}

	areas = append(areas, geo.AreaName{
		Parent: "world",
		Name:   "world",
	})

	// General stats

	bucket := int64(-1)
	monsIvIncr := 0
	monsSeenIncr := 0
	verifiedEncIncr := 0
	unverifiedEncIncr := 0
	verifiedEncSecTotalIncr := int64(0)
	timeToEncounter := int64(0)
	statsResetCountIncr := 0
	verifiedReEncounterIncr := 0
	verifiedReEncSecTotalIncr := int64(0)

	var encounterCacheVal *encounter_cache.Value

	populateEncounterCacheVal := func() {
		if encounterCacheVal == nil {
			encounterCacheVal = encounterCache.GetOrCreate(uint64(pokemon.Id))
		}
	}

	currentSeenType := pokemon.SeenType.ValueOrZero()
	oldSeenType := pokemon.oldValues.SeenType.ValueOrZero()

	if currentSeenType != oldSeenType {
		if oldSeenType == "" || oldSeenType == SeenType_NearbyStop || oldSeenType == SeenType_Cell {
			// New pokemon, or transition from cell or nearby stop

			if currentSeenType == SeenType_Wild {
				// transition to wild for the first time..
				populateEncounterCacheVal()
				encounterCacheVal.FirstEncounter = 0
				encounterCacheVal.FirstWild = pokemon.Updated.ValueOrZero()
				// This will be put into the cache later.
			}

			if currentSeenType == SeenType_Wild || currentSeenType == SeenType_Encounter {
				// transition to wild or encounter for the first time
				monsSeenIncr = 1
			}
		}

		if currentSeenType == SeenType_Encounter {
			populateEncounterCacheVal()
			if encounterCacheVal.FirstEncounter == 0 {
				// This is first encounter
				encounterCacheVal.FirstEncounter = pokemon.Updated.ValueOrZero()

				if encounterCacheVal.FirstWild > 0 {
					timeToEncounter = encounterCacheVal.FirstEncounter - encounterCacheVal.FirstWild
				}

				monsIvIncr = 1

				if pokemon.ExpireTimestampVerified {
					tth := pokemon.ExpireTimestamp.ValueOrZero() - pokemon.Updated.ValueOrZero() // relies on Updated being set
					bucket = tth / (5 * 60)
					if bucket > 11 {
						bucket = 11
					}
					verifiedEncIncr = 1
					verifiedEncSecTotalIncr = tth
				} else {
					unverifiedEncIncr = 1
				}
			} else {
				if pokemon.ExpireTimestampVerified {
					tth := pokemon.ExpireTimestamp.ValueOrZero() - pokemon.Updated.ValueOrZero() // relies on Updated being set

					verifiedReEncounterIncr = 1
					verifiedReEncSecTotalIncr = tth
				}
			}
		}
	}

	// If we have a cache entry, it means we updated it. So now let's store it.
	if encounterCacheVal != nil {
		encounterCache.Put(uint64(pokemon.Id), encounterCacheVal, pokemon.remainingDuration(now))
	}

	if (currentSeenType == SeenType_Wild && oldSeenType == SeenType_Encounter) ||
		(currentSeenType == SeenType_Encounter && oldSeenType == SeenType_Encounter &&
			pokemon.PokemonId != pokemon.oldValues.PokemonId) {
		// stats reset
		statsResetCountIncr = 1
	}

	locked := false

	var isHundo bool
	var isNundo bool

	if pokemon.Cp.Valid && pokemon.AtkIv.Valid && pokemon.DefIv.Valid && pokemon.StaIv.Valid {
		atk := pokemon.AtkIv.ValueOrZero()
		def := pokemon.DefIv.ValueOrZero()
		sta := pokemon.StaIv.ValueOrZero()
		if atk == 15 && def == 15 && sta == 15 {
			isHundo = true
		} else if atk == 0 && def == 0 && sta == 0 {
			isNundo = true
		}
	}

	for i := 0; i < len(areas); i++ {
		area := areas[i]
		fullAreaName := area.String()

		// Count stats (daily)

		if pokemon.isNewRecord() || pokemon.oldValues.Cp != pokemon.Cp { // pokemon is new or CP has changed (encountered or re-encountered)
			if !locked {
				pokemonStatsLock.Lock()
				locked = true
			}

			countStats, exists := pokemonCount[area]
			if !exists {
				countStats = &areaPokemonCountDetail{
					hundos:      make(map[pokemonForm]int),
					nundos:      make(map[pokemonForm]int),
					count:       make(map[pokemonForm]int),
					ivCount:     make(map[pokemonForm]int),
					shinyChecks: make(map[pokemonForm]shinyChecks),
				}
				pokemonCount[area] = countStats
			}

			formId := int(pokemon.Form.ValueOrZero())
			pf := pokemonForm{pokemonId: pokemon.PokemonId, formId: formId}

			if pokemon.isNewRecord() || pokemon.oldValues.PokemonId != pokemon.PokemonId { // pokemon is new or type has changed
				countStats.count[pf]++
				statsCollector.IncPokemonCountNew(fullAreaName)
				if pokemon.ExpireTimestampVerified {
					statsCollector.UpdateVerifiedTtl(area, pokemon.SeenType, pokemon.ExpireTimestamp)
				}
			}

			if pokemon.Cp.Valid {
				countStats.ivCount[pf]++
				statsCollector.IncPokemonCountIv(fullAreaName)
				if isHundo {
					statsCollector.IncPokemonCountHundo(fullAreaName)
					countStats.hundos[pf]++
				} else if isNundo {
					statsCollector.IncPokemonCountNundo(fullAreaName)
					countStats.nundos[pf]++
				}
			}
		}

		// Update record if we have a new stat
		if monsSeenIncr > 0 || monsIvIncr > 0 || verifiedEncIncr > 0 || unverifiedEncIncr > 0 ||
			bucket >= 0 || timeToEncounter > 0 || statsResetCountIncr > 0 ||
			verifiedReEncounterIncr > 0 {
			if locked == false {
				pokemonStatsLock.Lock()
				locked = true
			}

			areaStats := pokemonStats[area]
			if bucket >= 0 {
				areaStats.tthBucket[bucket]++
			}

			statsCollector.AddPokemonStatsResetCount(fullAreaName, float64(statsResetCountIncr))

			areaStats.monsIv += monsIvIncr
			areaStats.monsSeen += monsSeenIncr
			areaStats.verifiedEnc += verifiedEncIncr
			areaStats.unverifiedEnc += unverifiedEncIncr
			areaStats.verifiedEncSecTotal += verifiedEncSecTotalIncr
			areaStats.statsResetCount += statsResetCountIncr
			areaStats.verifiedReEncounter += verifiedReEncounterIncr
			areaStats.verifiedReEncSecTotal += verifiedReEncSecTotalIncr
			if timeToEncounter > 1 {
				areaStats.timeToEncounterCount++
				areaStats.timeToEncounterSum += timeToEncounter
			}
			pokemonStats[area] = areaStats
		}
	}

	if locked {
		pokemonStatsLock.Unlock()
	}

	// Hourly count stats - update separately with its own lock
	hourlyLocked := false
	for i := 0; i < len(areas); i++ {
		area := areas[i]

		if pokemon.isNewRecord() || pokemon.oldValues.Cp != pokemon.Cp { // pokemon is new or CP has changed (encountered or re-encountered)
			if !hourlyLocked {
				pokemonStatsHourlyLock.Lock()
				hourlyLocked = true
			}

			countStats, exists := pokemonCountHourly[area]
			if !exists {
				countStats = &areaPokemonCountDetail{
					hundos:      make(map[pokemonForm]int),
					nundos:      make(map[pokemonForm]int),
					count:       make(map[pokemonForm]int),
					ivCount:     make(map[pokemonForm]int),
					shinyChecks: make(map[pokemonForm]shinyChecks),
				}
				pokemonCountHourly[area] = countStats
			}

			formId := int(pokemon.Form.ValueOrZero())
			pf := pokemonForm{pokemonId: pokemon.PokemonId, formId: formId}

			if pokemon.isNewRecord() || pokemon.oldValues.PokemonId != pokemon.PokemonId { // pokemon is new or type has changed
				countStats.count[pf]++
			}

			if pokemon.Cp.Valid {
				countStats.ivCount[pf]++
				if isHundo {
					countStats.hundos[pf]++
				} else if isNundo {
					countStats.nundos[pf]++
				}
			}
		}
	}

	if hourlyLocked {
		pokemonStatsHourlyLock.Unlock()
	}

	// Excellent PVP stats (hourly only)
	// Only track if we have pvp results and thresholds are configured
	if pvpResults != nil && len(config.Config.Pvp.ExcellentPvpRankThreshold) > 0 {
		excellentPvpStatsHourlyLock.Lock()
		formId := int(pokemon.Form.ValueOrZero())
		pf := pokemonForm{pokemonId: pokemon.PokemonId, formId: formId}

		for i := 0; i < len(areas); i++ {
			area := areas[i]

			countStats, exists := excellentPvpCountHourly[area]
			if !exists {
				countStats = &areaPvpCountDetail{
					excellentPvp: make(map[pokemonForm]map[string]int),
				}
				excellentPvpCountHourly[area] = countStats
			}

			if countStats.excellentPvp[pf] == nil {
				countStats.excellentPvp[pf] = make(map[string]int)
			}

			// Check each league for excellent PVP rank
			for league, entries := range pvpResults {
				threshold, hasThreshold := config.Config.Pvp.ExcellentPvpRankThreshold[league]
				if !hasThreshold {
					continue
				}

				// Find the best rank for this league across all level caps
				var bestRank int16 = 4096
				for _, entry := range entries {
					if entry.Rank < bestRank {
						bestRank = entry.Rank
					}
				}

				// If the best rank is within the threshold, count it
				if bestRank <= threshold {
					countStats.excellentPvp[pf][league]++
				}
			}
		}
		excellentPvpStatsHourlyLock.Unlock()
	}
}

func updateRaidStats(gym *Gym, areas []geo.AreaName) {
	if len(areas) == 0 {
		areas = []geo.AreaName{{Parent: "unmatched", Name: "unmatched"}}
	}
	areas = append(areas, geo.AreaName{Parent: "world", Name: "world"})

	locked := false

	for i := 0; i < len(areas); i++ {
		area := areas[i]

		if gym.RaidPokemonId.ValueOrZero() > 0 &&
			(gym.newRecord || gym.oldValues.RaidPokemonId != gym.RaidPokemonId || gym.oldValues.RaidSpawnTimestamp != gym.RaidSpawnTimestamp) {

			if !locked {
				raidStatsLock.Lock()
				locked = true
			}

			if raidCount[area] == nil {
				raidCount[area] = make(map[int64]*areaRaidCountDetail)
			}
			countStats := raidCount[area]
			raidLevel := gym.RaidLevel.ValueOrZero()
			if countStats[raidLevel] == nil {
				countStats[raidLevel] = &areaRaidCountDetail{count: make(map[raidPokemonKey]int)}
			}
			rk := raidPokemonKey{
				pokemonId: int16(gym.RaidPokemonId.ValueOrZero()),
				formId:    int(gym.RaidPokemonForm.ValueOrZero()),
				tempEvoId: int(gym.RaidPokemonEvolution.ValueOrZero()),
			}
			countStats[raidLevel].count[rk]++
		}
	}

	if locked {
		raidStatsLock.Unlock()
	}

	// Hourly raid stats
	hourlyLocked := false

	for i := 0; i < len(areas); i++ {
		area := areas[i]

		if gym.RaidPokemonId.ValueOrZero() > 0 &&
			(gym.newRecord || gym.oldValues.RaidPokemonId != gym.RaidPokemonId || gym.oldValues.RaidEndTimestamp != gym.RaidEndTimestamp) {

			if !hourlyLocked {
				raidStatsHourlyLock.Lock()
				hourlyLocked = true
			}

			if raidCountHourly[area] == nil {
				raidCountHourly[area] = make(map[int64]*areaRaidCountDetail)
			}
			countStats := raidCountHourly[area]
			raidLevel := gym.RaidLevel.ValueOrZero()
			if countStats[raidLevel] == nil {
				countStats[raidLevel] = &areaRaidCountDetail{count: make(map[raidPokemonKey]int)}
			}
			rk := raidPokemonKey{
				pokemonId: int16(gym.RaidPokemonId.ValueOrZero()),
				formId:    int(gym.RaidPokemonForm.ValueOrZero()),
				tempEvoId: int(gym.RaidPokemonEvolution.ValueOrZero()),
			}
			countStats[raidLevel].count[rk]++
		}
	}

	if hourlyLocked {
		raidStatsHourlyLock.Unlock()
	}
}

func updateIncidentStats(incident *Incident, areas []geo.AreaName) {
	if len(areas) == 0 {
		areas = []geo.AreaName{
			{
				Parent: "unmatched",
				Name:   "unmatched",
			},
		}
	}

	areas = append(areas, geo.AreaName{
		Parent: "world",
		Name:   "world",
	})

	locked := false
	old := &incident.oldValues
	isNew := incident.IsNewRecord()

	// Loop though all areas (daily stats)
	for i := 0; i < len(areas); i++ {
		area := areas[i]

		// Check if StartTime has changed, then we can assume a new Incident has appeared.
		if isNew || old.StartTime != incident.StartTime {

			if !locked {
				incidentStatsLock.Lock()
				locked = true
			}

			invasionStats := invasionCount[area]
			if invasionStats == nil {
				invasionStats = &areaInvasionCountDetail{}
				invasionCount[area] = invasionStats
			}

			// Exclude Kecleon, Showcases and other UNSET characters for invasionStats.
			if incident.Character != 0 {
				invasionStats.count[incident.Character]++
			}
		}
	}

	if locked {
		incidentStatsLock.Unlock()
	}

	// Hourly invasion stats
	hourlyLocked := false

	for i := 0; i < len(areas); i++ {
		area := areas[i]

		if isNew || old.StartTime != incident.StartTime {

			if !hourlyLocked {
				incidentStatsHourlyLock.Lock()
				hourlyLocked = true
			}

			invasionStats := invasionCountHourly[area]
			if invasionStats == nil {
				invasionStats = &areaInvasionCountDetail{}
				invasionCountHourly[area] = invasionStats
			}

			if incident.Character != 0 {
				invasionStats.count[incident.Character]++
			}
		}
	}

	if hourlyLocked {
		incidentStatsHourlyLock.Unlock()
	}
}

func updateQuestStats(pokestop *Pokestop, haveAr bool, areas []geo.AreaName) {

	type areaQuestCount []struct {
		Info struct {
			PokemonID int `json:"pokemon_id"`
			ItemID    int `json:"item_id"`
			Amount    int `json:"amount"`
		} `json:"info"`
		Type int `json:"type"`
	}

	if len(areas) == 0 {
		areas = []geo.AreaName{
			{
				Parent: "unmatched",
				Name:   "unmatched",
			},
		}
	}

	areas = append(areas, geo.AreaName{
		Parent: "world",
		Name:   "world",
	})

	var err error
	var data areaQuestCount
	if !haveAr {
		err = json.Unmarshal([]byte(pokestop.AlternativeQuestRewards.String), &data)
	} else {
		err = json.Unmarshal([]byte(pokestop.QuestRewards.String), &data)
	}

	if err != nil {
		log.Errorf("updateQuestStats - couldn't unpack pokestop data for %s", pokestop.Id)
		return
	}

	locked := false

	// Loop though all areas (daily stats)
	for i := 0; i < len(areas); i++ {
		area := areas[i]

		if !locked {
			questStatsLock.Lock()
			locked = true
		}

		countStats := questCount[area]
		if countStats == nil {
			countStats = make(map[int]areaQuestCountDetail)
			questCount[area] = countStats
		}

		for _, item := range data {
			var countQuests = questCount[area][item.Type]

			if item.Info.PokemonID != 0 { // update stats with pokemonId and amount
				if countQuests.pokemonDetails[item.Info.PokemonID] == nil {
					countQuests.pokemonDetails[item.Info.PokemonID] = make(map[int]int)
				}
				countQuests.pokemonDetails[item.Info.PokemonID][item.Info.Amount]++
			} else if item.Info.ItemID != 0 || item.Info.Amount != 0 { // update stats when itemId or amount (per type) is >0
				if countQuests.itemDetails[item.Info.ItemID] == nil {
					countQuests.itemDetails[item.Info.ItemID] = make(map[int]int)
				}
				countQuests.itemDetails[item.Info.ItemID][item.Info.Amount]++
			} else {
				countQuests.count++
			}

			questCount[area][item.Type] = countQuests
		}

	}

	if locked {
		questStatsLock.Unlock()
	}

	// Hourly quest stats
	hourlyLocked := false

	for i := 0; i < len(areas); i++ {
		area := areas[i]

		if !hourlyLocked {
			questStatsHourlyLock.Lock()
			hourlyLocked = true
		}

		countStats := questCountHourly[area]
		if countStats == nil {
			countStats = make(map[int]areaQuestCountDetail)
			questCountHourly[area] = countStats
		}

		for _, item := range data {
			var countQuests = questCountHourly[area][item.Type]

			if item.Info.PokemonID != 0 {
				if countQuests.pokemonDetails[item.Info.PokemonID] == nil {
					countQuests.pokemonDetails[item.Info.PokemonID] = make(map[int]int)
				}
				countQuests.pokemonDetails[item.Info.PokemonID][item.Info.Amount]++
			} else if item.Info.ItemID != 0 || item.Info.Amount != 0 {
				if countQuests.itemDetails[item.Info.ItemID] == nil {
					countQuests.itemDetails[item.Info.ItemID] = make(map[int]int)
				}
				countQuests.itemDetails[item.Info.ItemID][item.Info.Amount]++
			} else {
				countQuests.count++
			}

			questCountHourly[area][item.Type] = countQuests
		}
	}

	if hourlyLocked {
		questStatsHourlyLock.Unlock()
	}
}

type pokemonStatsDbRow struct {
	DateTime              int64  `db:"datetime"`
	Area                  string `db:"area"`
	Fence                 string `db:"fence"`
	TotMon                int    `db:"totMon"`
	IvMon                 int    `db:"ivMon"`
	VerifiedEnc           int    `db:"verifiedEnc"`
	UnverifiedEnc         int    `db:"unverifiedEnc"`
	VerifiedReEnc         int    `db:"verifiedReEnc"`
	VerifiedWild          int    `db:"verifiedWild"`
	EncSecLeft            int64  `db:"encSecLeft"`
	EncTthMax5            int    `db:"encTthMax5"`
	EncTth5to10           int    `db:"encTth5to10"`
	EncTth10to15          int    `db:"encTth10to15"`
	EncTth15to20          int    `db:"encTth15to20"`
	EncTth20to25          int    `db:"encTth20to25"`
	EncTth25to30          int    `db:"encTth25to30"`
	EncTth30to35          int    `db:"encTth30to35"`
	EncTth35to40          int    `db:"encTth35to40"`
	EncTth40to45          int    `db:"encTth40to45"`
	EncTth45to50          int    `db:"encTth45to50"`
	EncTth50to55          int    `db:"encTth50to55"`
	EncTthMin55           int    `db:"encTthMin55"`
	ResetMon              int    `db:"resetMon"`
	ReencounterTthLeft    int64  `db:"re_encSecLeft"`
	NumWildEncounters     int    `db:"numWiEnc"`
	SumSecWildToEncounter int64  `db:"secWiEnc"`
}

func logPokemonStats(statsDb *sqlx.DB) {
	pokemonStatsLock.Lock()
	log.Infof("STATS: Write area stats")

	currentStats := pokemonStats
	pokemonStats = make(map[geo.AreaName]areaStatsCount) // clear stats
	pokemonStatsLock.Unlock()
	go func() {
		var rows []pokemonStatsDbRow
		t := time.Now().Truncate(time.Minute).Unix()
		for area, stats := range currentStats {
			rows = append(rows, pokemonStatsDbRow{
				DateTime:      t,
				Area:          area.Parent,
				Fence:         area.Name,
				TotMon:        stats.monsSeen,
				IvMon:         stats.monsIv,
				VerifiedEnc:   stats.verifiedEnc,
				VerifiedReEnc: stats.verifiedReEncounter,
				UnverifiedEnc: stats.unverifiedEnc,

				EncSecLeft:   stats.verifiedEncSecTotal,
				EncTthMax5:   stats.tthBucket[0],
				EncTth5to10:  stats.tthBucket[1],
				EncTth10to15: stats.tthBucket[2],
				EncTth15to20: stats.tthBucket[3],
				EncTth20to25: stats.tthBucket[4],
				EncTth25to30: stats.tthBucket[5],
				EncTth30to35: stats.tthBucket[6],
				EncTth35to40: stats.tthBucket[7],
				EncTth40to45: stats.tthBucket[8],
				EncTth45to50: stats.tthBucket[9],
				EncTth50to55: stats.tthBucket[10],
				EncTthMin55:  stats.tthBucket[11],

				ResetMon:              stats.statsResetCount,
				ReencounterTthLeft:    stats.verifiedReEncSecTotal,
				NumWildEncounters:     stats.timeToEncounterCount,
				SumSecWildToEncounter: stats.timeToEncounterSum,
			})
		}

		if len(rows) > 0 {
			_, err := statsDb.NamedExec(
				"INSERT INTO pokemon_area_stats "+
					"(datetime, area, fence, totMon, ivMon, verifiedEnc, unverifiedEnc, verifiedReEnc, encSecLeft, encTthMax5, encTth5to10, encTth10to15, encTth15to20, encTth20to25, encTth25to30, encTth30to35, encTth35to40, encTth40to45, encTth45to50, encTth50to55, encTthMin55, resetMon, re_encSecLeft, numWiEnc, secWiEnc) "+
					"VALUES (:datetime, :area, :fence, :totMon, :ivMon, :verifiedEnc, :unverifiedEnc, :verifiedReEnc, :encSecLeft, :encTthMax5, :encTth5to10, :encTth10to15, :encTth15to20, :encTth20to25, :encTth25to30, :encTth30to35, :encTth35to40, :encTth40to45, :encTth45to50, :encTth50to55, :encTthMin55, :resetMon, :re_encSecLeft, :numWiEnc, :secWiEnc)",
				rows)
			if err != nil {
				log.Errorf("Error inserting pokemon_area_stats: %v", err)
			}
		}
	}()

}

type pokemonCountDbRow struct {
	Date      string `db:"date"`
	Area      string `db:"area"`
	Fence     string `db:"fence"`
	PokemonId int    `db:"pokemon_id"`
	FormId    int    `db:"form_id"`
	Count     int    `db:"count"`
}

type pokemonShinyCountDbRow struct {
	Date      string `db:"date"`
	Area      string `db:"area"`
	Fence     string `db:"fence"`
	PokemonId int    `db:"pokemon_id"`
	FormId    int    `db:"form_id"`
	Count     int    `db:"count"`
	Total     int    `db:"total"`
}

func logPokemonCount(statsDb *sqlx.DB) {

	log.Infof("STATS: Update pokemon count tables")

	pokemonStatsLock.Lock()
	currentStats := pokemonCount
	pokemonCount = make(map[geo.AreaName]*areaPokemonCountDetail) // clear stats
	pokemonStatsLock.Unlock()

	go func() {
		var hundoRows []pokemonCountDbRow
		var shinyRows []pokemonShinyCountDbRow
		var nundoRows []pokemonCountDbRow
		var ivRows []pokemonCountDbRow
		var allRows []pokemonCountDbRow

		t := time.Now().In(time.Local)
		midnightString := t.Format("2006-01-02")

		for area, stats := range currentStats {
			addRows := func(rows *[]pokemonCountDbRow, pf pokemonForm, count int) {
				*rows = append(*rows, pokemonCountDbRow{
					Date:      midnightString,
					Area:      area.Parent,
					Fence:     area.Name,
					PokemonId: int(pf.pokemonId),
					FormId:    pf.formId,
					Count:     count,
				})
			}

			for pf, count := range stats.count {
				if count > 0 {
					addRows(&allRows, pf, count)
				}
			}

			for pf, count := range stats.ivCount {
				if count > 0 {
					addRows(&ivRows, pf, count)
				}
			}

			for pf, count := range stats.hundos {
				if count > 0 {
					addRows(&hundoRows, pf, count)
				}
			}

			for pf, count := range stats.nundos {
				if count > 0 {
					addRows(&nundoRows, pf, count)
				}
			}

			for pf, checks := range stats.shinyChecks {
				if checks.total > 0 {
					shinyRows = append(shinyRows, pokemonShinyCountDbRow{
						Date:      midnightString,
						Area:      area.Parent,
						Fence:     area.Name,
						PokemonId: int(pf.pokemonId),
						FormId:    pf.formId,
						Count:     checks.shiny,
						Total:     checks.total,
					})
				}
			}
		}

		updatePokemonStatsCount := func(table string, rows []pokemonCountDbRow) {
			if len(rows) > 0 {
				chunkSize := 100

				for i := 0; i < len(rows); i += chunkSize {
					end := i + chunkSize

					// necessary check to avoid slicing beyond slice capacity
					if end > len(rows) {
						end = len(rows)
					}

					rowsToWrite := rows[i:end]

					_, err := statsDb.NamedExec(
						fmt.Sprintf("INSERT INTO %s (date, area, fence, pokemon_id, form_id, `count`)"+
							" VALUES (:date, :area, :fence, :pokemon_id, :form_id, :count)"+
							" ON DUPLICATE KEY UPDATE `count` = `count` + VALUES(`count`);", table),
						rowsToWrite,
					)
					if err != nil {
						log.Errorf("Error inserting %s: %v", table, err)
					}
				}
			}
		}

		updatePokemonStatsCount("pokemon_stats", allRows)
		updatePokemonStatsCount("pokemon_iv_stats", ivRows)
		updatePokemonStatsCount("pokemon_hundo_stats", hundoRows)
		updatePokemonStatsCount("pokemon_nundo_stats", nundoRows)

		if rows := shinyRows; len(rows) > 0 {
			chunkSize := batchInsertSize

			for i := 0; i < len(rows); i += chunkSize {
				end := i + chunkSize

				// necessary check to avoid slicing beyond slice capacity
				if end > len(rows) {
					end = len(rows)
				}

				rowsToWrite := rows[i:end]

				_, err := statsDb.NamedExec(
					"INSERT INTO pokemon_shiny_stats (date, area, fence, pokemon_id, form_id, `count`, total)"+
						" VALUES (:date, :area, :fence, :pokemon_id, :form_id, :count, :total)"+
						" ON DUPLICATE KEY UPDATE `count` = `count` + VALUES(`count`), total = total + VALUES(total);",
					rowsToWrite,
				)
				if err != nil {
					log.Errorf("Error inserting pokemon_shiny_stats: %v", err)
				}
			}
		}
	}()

}

type raidStatsDbRow struct {
	Date      string `db:"date"`
	Area      string `db:"area"`
	Fence     string `db:"fence"`
	Level     int64  `db:"level"`
	PokemonId int    `db:"pokemon_id"`
	FormId    int    `db:"form_id"`
	TempEvoId int    `db:"temp_evo_id"`
	Count     int    `db:"count"`
}

func logRaidStats(statsDb *sqlx.DB) {
	raidStatsLock.Lock()
	log.Infof("STATS: Write raid stats")

	currentStats := raidCount
	raidCount = make(map[geo.AreaName]map[int64]*areaRaidCountDetail) // clear stats
	raidStatsLock.Unlock()

	go func() {
		var rows []raidStatsDbRow

		t := time.Now().In(time.Local)
		midnightString := t.Format("2006-01-02")

		for area, stats := range currentStats {
			addRows := func(rows *[]raidStatsDbRow, level int64, rk raidPokemonKey, count int) {
				*rows = append(*rows, raidStatsDbRow{
					Date:      midnightString,
					Area:      area.Parent,
					Fence:     area.Name,
					Level:     level,
					PokemonId: int(rk.pokemonId),
					FormId:    rk.formId,
					TempEvoId: rk.tempEvoId,
					Count:     count,
				})
			}

			for level, raidDetail := range stats {
				if raidDetail.count == nil {
					continue // nothing to do
				}
				for rk, count := range raidDetail.count {
					if count > 0 {
						addRows(&rows, level, rk, count)
					}
				}
			}
		}

		for i := 0; i < len(rows); i += batchInsertSize {
			end := i + batchInsertSize
			if end > len(rows) {
				end = len(rows)
			}

			batchRows := rows[i:end]
			_, err := statsDb.NamedExec(
				"INSERT INTO raid_stats "+
					"(date, area, fence, level, pokemon_id, form_id, temp_evo_id, `count`)"+
					" VALUES (:date, :area, :fence, :level, :pokemon_id, :form_id, :temp_evo_id, :count)"+
					" ON DUPLICATE KEY UPDATE `count` = `count` + VALUES(`count`);", batchRows)
			if err != nil {
				log.Errorf("Error inserting raid_stats: %v", err)
			}
		}
	}()
}

type invasionStatsDbRow struct {
	Date      string `db:"date"`
	Area      string `db:"area"`
	Fence     string `db:"fence"`
	Character int    `db:"character"`
	Count     int    `db:"count"`
}

func logInvasionStats(statsDb *sqlx.DB) {
	incidentStatsLock.Lock()
	log.Infof("STATS: Write invasion stats")

	currentStats := invasionCount
	invasionCount = make(map[geo.AreaName]*areaInvasionCountDetail) // clear stats
	incidentStatsLock.Unlock()

	go func() {
		var rows []invasionStatsDbRow

		t := time.Now().In(time.Local)
		midnightString := t.Format("2006-01-02")

		for area, stats := range currentStats {
			addRows := func(rows *[]invasionStatsDbRow, character int, count int) {
				*rows = append(*rows, invasionStatsDbRow{
					Date:      midnightString,
					Area:      area.Parent,
					Fence:     area.Name,
					Character: character,
					Count:     count,
				})
			}

			for character, count := range stats.count {
				if count > 0 {
					addRows(&rows, character, count)
				}
			}
		}

		for i := 0; i < len(rows); i += batchInsertSize {
			end := i + batchInsertSize
			if end > len(rows) {
				end = len(rows)
			}

			batchRows := rows[i:end]
			_, err := statsDb.NamedExec(
				"INSERT INTO invasion_stats "+
					"(date, area, fence, `character`, `count`)"+
					" VALUES (:date, :area, :fence, :character, :count)"+
					" ON DUPLICATE KEY UPDATE `count` = `count` + VALUES(`count`);", batchRows)
			if err != nil {
				log.Errorf("Error inserting invasion_stats: %v", err)
			}
		}
	}()
}

type questStatsDbRow struct {
	Date       string `db:"date"`
	Area       string `db:"area"`
	Fence      string `db:"fence"`
	RewardType int    `db:"reward_type"`
	PokemonId  int    `db:"pokemon_id"`
	ItemId     int    `db:"item_id"`
	ItemAmount int    `db:"item_amount"`
	Count      int    `db:"count"`
}

func logQuestStats(statsDb *sqlx.DB) {
	questStatsLock.Lock()
	log.Infof("STATS: Write quest stats")

	currentStats := questCount
	questCount = make(map[geo.AreaName]map[int]areaQuestCountDetail) // clear stats
	questStatsLock.Unlock()

	go func() {
		var rows []questStatsDbRow

		t := time.Now().In(time.Local)
		midnightString := t.Format("2006-01-02")

		for area, stats := range currentStats {
			addRows := func(rows *[]questStatsDbRow, reward_type int, pokemon_id int, item_id int, item_amount int, count int) {
				*rows = append(*rows, questStatsDbRow{
					Date:       midnightString,
					Area:       area.Parent,
					Fence:      area.Name,
					RewardType: reward_type,
					PokemonId:  pokemon_id,
					ItemId:     item_id,
					ItemAmount: item_amount,
					Count:      count,
				})
			}

			for reward_type := range stats {

				// If count is higher then 0, then we can assume pokemonId & itemId has not been used.
				if stats[reward_type].count > 0 {
					addRows(&rows, reward_type, 0, 0, 0, stats[reward_type].count)
				} else {

					for pokemonId, amounts := range stats[reward_type].pokemonDetails {
						for megaEnergyAmount, count := range amounts {
							if count > 0 {
								addRows(&rows, reward_type, pokemonId, 0, megaEnergyAmount, count)
							}
						}
					}

					for itemId, amounts := range stats[reward_type].itemDetails {
						for itemAmount, count := range amounts {
							if count > 0 {
								addRows(&rows, reward_type, 0, itemId, itemAmount, count)
							}
						}
					}
				}
			}
		}

		for i := 0; i < len(rows); i += batchInsertSize {
			end := i + batchInsertSize
			if end > len(rows) {
				end = len(rows)
			}

			batchRows := rows[i:end]
			_, err := statsDb.NamedExec(
				"INSERT INTO quest_stats "+
					"(date, area, fence, reward_type, pokemon_id, item_id, item_amount, `count`) "+
					"VALUES (:date, :area, :fence, :reward_type, :pokemon_id, :item_id, :item_amount, :count) "+
					"ON DUPLICATE KEY UPDATE `count` = `count` + VALUES(`count`);",
				batchRows,
			)
			if err != nil {
				log.Errorf("Error inserting quest_stats: %v", err)
			}
		}
	}()
}

// Hourly stats DB row types
type pokemonCountHourlyDbRow struct {
	DateTime  string `db:"datetime"`
	Area      string `db:"area"`
	Fence     string `db:"fence"`
	PokemonId int    `db:"pokemon_id"`
	FormId    int    `db:"form_id"`
	Count     int    `db:"count"`
}

type pokemonShinyCountHourlyDbRow struct {
	DateTime  string `db:"datetime"`
	Area      string `db:"area"`
	Fence     string `db:"fence"`
	PokemonId int    `db:"pokemon_id"`
	FormId    int    `db:"form_id"`
	Count     int    `db:"count"`
	Total     int    `db:"total"`
}

type raidStatsHourlyDbRow struct {
	DateTime  string `db:"datetime"`
	Area      string `db:"area"`
	Fence     string `db:"fence"`
	Level     int64  `db:"level"`
	PokemonId int    `db:"pokemon_id"`
	FormId    int    `db:"form_id"`
	Count     int    `db:"count"`
}

type invasionStatsHourlyDbRow struct {
	DateTime  string `db:"datetime"`
	Area      string `db:"area"`
	Fence     string `db:"fence"`
	Character int    `db:"character"`
	Count     int    `db:"count"`
}

type questStatsHourlyDbRow struct {
	DateTime   string `db:"datetime"`
	Area       string `db:"area"`
	Fence      string `db:"fence"`
	RewardType int    `db:"reward_type"`
	PokemonId  int    `db:"pokemon_id"`
	ItemId     int    `db:"item_id"`
	ItemAmount int    `db:"item_amount"`
	Count      int    `db:"count"`
}

func logPokemonCountHourly(statsDb *sqlx.DB) {

	log.Infof("STATS: Update hourly pokemon count tables")

	pokemonStatsHourlyLock.Lock()
	currentStats := pokemonCountHourly
	pokemonCountHourly = make(map[geo.AreaName]*areaPokemonCountDetail) // clear stats
	pokemonStatsHourlyLock.Unlock()

	go func() {
		var hundoRows []pokemonCountHourlyDbRow
		var shinyRows []pokemonShinyCountHourlyDbRow
		var nundoRows []pokemonCountHourlyDbRow
		var ivRows []pokemonCountHourlyDbRow
		var allRows []pokemonCountHourlyDbRow

		t := time.Now().In(time.Local).Truncate(time.Hour)
		hourString := t.Format("2006-01-02 15:04:05")

		for area, stats := range currentStats {
			addRows := func(rows *[]pokemonCountHourlyDbRow, pf pokemonForm, count int) {
				*rows = append(*rows, pokemonCountHourlyDbRow{
					DateTime:  hourString,
					Area:      area.Parent,
					Fence:     area.Name,
					PokemonId: int(pf.pokemonId),
					FormId:    pf.formId,
					Count:     count,
				})
			}

			for pf, count := range stats.count {
				if count > 0 {
					addRows(&allRows, pf, count)
				}
			}

			for pf, count := range stats.ivCount {
				if count > 0 {
					addRows(&ivRows, pf, count)
				}
			}

			for pf, count := range stats.hundos {
				if count > 0 {
					addRows(&hundoRows, pf, count)
				}
			}

			for pf, count := range stats.nundos {
				if count > 0 {
					addRows(&nundoRows, pf, count)
				}
			}

			for pf, checks := range stats.shinyChecks {
				if checks.total > 0 {
					shinyRows = append(shinyRows, pokemonShinyCountHourlyDbRow{
						DateTime:  hourString,
						Area:      area.Parent,
						Fence:     area.Name,
						PokemonId: int(pf.pokemonId),
						FormId:    pf.formId,
						Count:     checks.shiny,
						Total:     checks.total,
					})
				}
			}
		}

		updatePokemonStatsCountHourly := func(table string, rows []pokemonCountHourlyDbRow) {
			if len(rows) > 0 {
				chunkSize := 100

				for i := 0; i < len(rows); i += chunkSize {
					end := i + chunkSize

					if end > len(rows) {
						end = len(rows)
					}

					rowsToWrite := rows[i:end]

					_, err := statsDb.NamedExec(
						fmt.Sprintf("INSERT INTO %s (datetime, area, fence, pokemon_id, form_id, `count`)"+
							" VALUES (:datetime, :area, :fence, :pokemon_id, :form_id, :count)"+
							" ON DUPLICATE KEY UPDATE `count` = `count` + VALUES(`count`);", table),
						rowsToWrite,
					)
					if err != nil {
						log.Errorf("Error inserting %s: %v", table, err)
					}
				}
			}
		}

		updatePokemonStatsCountHourly("pokemon_stats_hourly", allRows)
		updatePokemonStatsCountHourly("pokemon_iv_stats_hourly", ivRows)
		updatePokemonStatsCountHourly("pokemon_hundo_stats_hourly", hundoRows)
		updatePokemonStatsCountHourly("pokemon_nundo_stats_hourly", nundoRows)

		if rows := shinyRows; len(rows) > 0 {
			chunkSize := batchInsertSize

			for i := 0; i < len(rows); i += chunkSize {
				end := i + chunkSize

				if end > len(rows) {
					end = len(rows)
				}

				rowsToWrite := rows[i:end]

				_, err := statsDb.NamedExec(
					"INSERT INTO pokemon_shiny_stats_hourly (datetime, area, fence, pokemon_id, form_id, `count`, total)"+
						" VALUES (:datetime, :area, :fence, :pokemon_id, :form_id, :count, :total)"+
						" ON DUPLICATE KEY UPDATE `count` = `count` + VALUES(`count`), total = total + VALUES(total);",
					rowsToWrite,
				)
				if err != nil {
					log.Errorf("Error inserting pokemon_shiny_stats_hourly: %v", err)
				}
			}
		}
	}()
}

func logRaidStatsHourly(statsDb *sqlx.DB) {
	raidStatsHourlyLock.Lock()
	log.Infof("STATS: Write hourly raid stats")

	currentStats := raidCountHourly
	raidCountHourly = make(map[geo.AreaName]map[int64]*areaRaidCountDetail) // clear stats
	raidStatsHourlyLock.Unlock()

	go func() {
		var rows []raidStatsHourlyDbRow

		t := time.Now().In(time.Local).Truncate(time.Hour)
		hourString := t.Format("2006-01-02 15:04:05")

		for area, stats := range currentStats {
			addRows := func(rows *[]raidStatsHourlyDbRow, level int64, pokemonId int, formId int, count int) {
				*rows = append(*rows, raidStatsHourlyDbRow{
					DateTime:  hourString,
					Area:      area.Parent,
					Fence:     area.Name,
					Level:     level,
					PokemonId: pokemonId,
					FormId:    formId,
					Count:     count,
				})
			}

			for level, raidDetail := range stats {
				if raidDetail.count == nil {
					continue
				}
				for pf, count := range raidDetail.count {
					if count > 0 {
						addRows(&rows, level, int(pf.pokemonId), pf.formId, count)
					}
				}
			}
		}

		for i := 0; i < len(rows); i += batchInsertSize {
			end := i + batchInsertSize
			if end > len(rows) {
				end = len(rows)
			}

			batchRows := rows[i:end]
			_, err := statsDb.NamedExec(
				"INSERT INTO raid_stats_hourly "+
					"(datetime, area, fence, level, pokemon_id, form_id, `count`)"+
					" VALUES (:datetime, :area, :fence, :level, :pokemon_id, :form_id, :count)"+
					" ON DUPLICATE KEY UPDATE `count` = `count` + VALUES(`count`);", batchRows)
			if err != nil {
				log.Errorf("Error inserting raid_stats_hourly: %v", err)
			}
		}
	}()
}

func logInvasionStatsHourly(statsDb *sqlx.DB) {
	incidentStatsHourlyLock.Lock()
	log.Infof("STATS: Write hourly invasion stats")

	currentStats := invasionCountHourly
	invasionCountHourly = make(map[geo.AreaName]*areaInvasionCountDetail) // clear stats
	incidentStatsHourlyLock.Unlock()

	go func() {
		var rows []invasionStatsHourlyDbRow

		t := time.Now().In(time.Local).Truncate(time.Hour)
		hourString := t.Format("2006-01-02 15:04:05")

		for area, stats := range currentStats {
			addRows := func(rows *[]invasionStatsHourlyDbRow, character int, count int) {
				*rows = append(*rows, invasionStatsHourlyDbRow{
					DateTime:  hourString,
					Area:      area.Parent,
					Fence:     area.Name,
					Character: character,
					Count:     count,
				})
			}

			for character, count := range stats.count {
				if count > 0 {
					addRows(&rows, character, count)
				}
			}
		}

		for i := 0; i < len(rows); i += batchInsertSize {
			end := i + batchInsertSize
			if end > len(rows) {
				end = len(rows)
			}

			batchRows := rows[i:end]
			_, err := statsDb.NamedExec(
				"INSERT INTO invasion_stats_hourly "+
					"(datetime, area, fence, `character`, `count`)"+
					" VALUES (:datetime, :area, :fence, :character, :count)"+
					" ON DUPLICATE KEY UPDATE `count` = `count` + VALUES(`count`);", batchRows)
			if err != nil {
				log.Errorf("Error inserting invasion_stats_hourly: %v", err)
			}
		}
	}()
}

func logQuestStatsHourly(statsDb *sqlx.DB) {
	questStatsHourlyLock.Lock()
	log.Infof("STATS: Write hourly quest stats")

	currentStats := questCountHourly
	questCountHourly = make(map[geo.AreaName]map[int]areaQuestCountDetail) // clear stats
	questStatsHourlyLock.Unlock()

	go func() {
		var rows []questStatsHourlyDbRow

		t := time.Now().In(time.Local).Truncate(time.Hour)
		hourString := t.Format("2006-01-02 15:04:05")

		for area, stats := range currentStats {
			addRows := func(rows *[]questStatsHourlyDbRow, reward_type int, pokemon_id int, item_id int, item_amount int, count int) {
				*rows = append(*rows, questStatsHourlyDbRow{
					DateTime:   hourString,
					Area:       area.Parent,
					Fence:      area.Name,
					RewardType: reward_type,
					PokemonId:  pokemon_id,
					ItemId:     item_id,
					ItemAmount: item_amount,
					Count:      count,
				})
			}

			for reward_type := range stats {

				if stats[reward_type].count > 0 {
					addRows(&rows, reward_type, 0, 0, 0, stats[reward_type].count)
				} else {

					for pokemonId, amounts := range stats[reward_type].pokemonDetails {
						for megaEnergyAmount, count := range amounts {
							if count > 0 {
								addRows(&rows, reward_type, pokemonId, 0, megaEnergyAmount, count)
							}
						}
					}

					for itemId, amounts := range stats[reward_type].itemDetails {
						for itemAmount, count := range amounts {
							if count > 0 {
								addRows(&rows, reward_type, 0, itemId, itemAmount, count)
							}
						}
					}
				}
			}
		}

		for i := 0; i < len(rows); i += batchInsertSize {
			end := i + batchInsertSize
			if end > len(rows) {
				end = len(rows)
			}

			batchRows := rows[i:end]
			_, err := statsDb.NamedExec(
				"INSERT INTO quest_stats_hourly "+
					"(datetime, area, fence, reward_type, pokemon_id, item_id, item_amount, `count`) "+
					"VALUES (:datetime, :area, :fence, :reward_type, :pokemon_id, :item_id, :item_amount, :count) "+
					"ON DUPLICATE KEY UPDATE `count` = `count` + VALUES(`count`);",
				batchRows,
			)
			if err != nil {
				log.Errorf("Error inserting quest_stats_hourly: %v", err)
			}
		}
	}()
}

// Excellent PVP stats DB row type
type excellentPvpStatsHourlyDbRow struct {
	DateTime  string `db:"datetime"`
	Area      string `db:"area"`
	Fence     string `db:"fence"`
	PokemonId int    `db:"pokemon_id"`
	FormId    int    `db:"form_id"`
	League    string `db:"league"`
	Count     int    `db:"count"`
}

func logExcellentPvpStatsHourly(statsDb *sqlx.DB) {
	excellentPvpStatsHourlyLock.Lock()
	log.Infof("STATS: Write hourly excellent PVP stats")

	currentStats := excellentPvpCountHourly
	excellentPvpCountHourly = make(map[geo.AreaName]*areaPvpCountDetail) // clear stats
	excellentPvpStatsHourlyLock.Unlock()

	go func() {
		var rows []excellentPvpStatsHourlyDbRow

		t := time.Now().In(time.Local).Truncate(time.Hour)
		hourString := t.Format("2006-01-02 15:04:05")

		for area, stats := range currentStats {
			for pf, leagueCounts := range stats.excellentPvp {
				for league, count := range leagueCounts {
					if count > 0 {
						rows = append(rows, excellentPvpStatsHourlyDbRow{
							DateTime:  hourString,
							Area:      area.Parent,
							Fence:     area.Name,
							PokemonId: int(pf.pokemonId),
							FormId:    pf.formId,
							League:    league,
							Count:     count,
						})
					}
				}
			}
		}

		for i := 0; i < len(rows); i += batchInsertSize {
			end := i + batchInsertSize
			if end > len(rows) {
				end = len(rows)
			}

			batchRows := rows[i:end]
			_, err := statsDb.NamedExec(
				"INSERT INTO pokemon_excellent_pvp_stats_hourly "+
					"(datetime, area, fence, pokemon_id, form_id, league, `count`) "+
					"VALUES (:datetime, :area, :fence, :pokemon_id, :form_id, :league, :count) "+
					"ON DUPLICATE KEY UPDATE `count` = `count` + VALUES(`count`);",
				batchRows,
			)
			if err != nil {
				log.Errorf("Error inserting pokemon_excellent_pvp_stats_hourly: %v", err)
			}
		}
	}()
}
