package main

const (
	protocolVersion = 84

	maxClients       = 64
	gentityNumBits   = 10
	maxGentities     = 1 << gentityNumBits
	removedEntityNum = maxGentities - 1
	entityNumWorld   = maxGentities - 2

	maxConfigStrings = 1024
	maxParseEntities = 2048

	packetBackup = 32
	packetMask   = packetBackup - 1

	maxStats      = 16
	maxPersistant = 16
	maxPowerups   = 16
	maxHoldable   = 16
	maxAmmoGroups = 4
	ammoPerGroup  = 16

	maxMsgLen       = 32768
	maxStringChars  = 1024
	bigInfoString   = 8192
	floatIntBits    = 13
	floatIntBias    = 1 << (floatIntBits - 1)
	maxParseBacklog = maxParseEntities - 128
)

const (
	svcBad = iota
	svcNop
	svcGamestate
	svcConfigstring
	svcBaseline
	svcServerCommand
	svcDownload
	svcSnapshot
	svcEOF
)

const (
	csLevelStartTime = 11
	csPlayers        = 689
)

const (
	teamFree = iota
	teamAxis
	teamAllies
	teamSpectator
)

const (
	etEvents   = 62
	evObituary = 68
)

const (
	entityFieldCount      = 71
	playerStateFieldCount = 77
)

const (
	fieldEntityType      = 0
	fieldOtherEntityNum  = 34
	fieldOtherEntityNum2 = 35
	fieldLoopSound       = 37
	fieldWeapon          = 57
)

const (
	weaponNone  = 0
	weaponKnife = 1
	weaponLuger = 2
	weaponMP40  = 3
)

var entityFieldBits = [entityFieldCount]int{
	8, 24, 8, 32, 32, 0, 0, 0, 0, 0, 0, 8, 32, 32, 0, 0, 0, 0, 0, 0,
	32, 32, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 10, 10, 10, 8, 32, 32,
	9, 9, 16, 8, 24, 10, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 16, 8, 10, 10,
	10, 32, 32, 32, 8, 8, 32, 32, 32, 4, 2,
}

var playerStateFieldBits = [playerStateFieldCount]int{
	32, 8, 8, 16, -16, 0, 0, 0, 0, 0, 0, -16, -16, -16, 16, 0, 16, 16, 16, 16,
	10, 16, 16, 10, 10, 8, 24, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 32, 32, 7,
	4, 10, 0, 0, 0, -8, 8, 8, 8, 8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	0, 0, 0, 0, 8, 16, 8, 8, 32, 8, 8, 8, 8, 32, 8, 8, 2,
}

var weaponNames = [...]string{
	"NONE",
	"KNIFE",
	"LUGER",
	"MP40",
	"GRENADE_LAUNCHER",
	"PANZERFAUST",
	"FLAMETHROWER",
	"COLT",
	"THOMPSON",
	"GRENADE_PINEAPPLE",
	"STEN",
	"MEDIC_SYRINGE",
	"AMMO",
	"ARTY",
	"SILENCER",
	"DYNAMITE",
	"SMOKETRAIL",
	"MAPMORTAR",
	"VERYBIGEXPLOSION",
	"MEDKIT",
	"BINOCULARS",
	"PLIERS",
	"SMOKE_MARKER",
	"KAR98",
	"CARBINE",
	"GARAND",
	"LANDMINE",
	"SATCHEL",
	"SATCHEL_DET",
	"SMOKE_BOMB",
	"MOBILE_MG42",
	"K43",
	"FG42",
	"DUMMY_MG42",
	"MORTAR",
	"AKIMBO_COLT",
	"AKIMBO_LUGER",
	"GPG40",
	"M7",
	"SILENCED_COLT",
	"GARAND_SCOPE",
	"K43_SCOPE",
	"FG42_SCOPE",
	"MORTAR_SET",
	"MEDIC_ADRENALINE",
	"AKIMBO_SILENCEDCOLT",
	"AKIMBO_SILENCEDLUGER",
	"MOBILE_MG42_SET",
	"KNIFE_KABAR",
	"MOBILE_BROWNING",
	"MOBILE_BROWNING_SET",
	"MORTAR2",
	"MORTAR2_SET",
	"BAZOOKA",
	"MP34",
	"AIRSTRIKE",
}

type playerInfo struct {
	Name string
	Team int
}

// entityState stores the network fields in the exact order used by msg.c.
// Float fields are stored as their IEEE-754 bit pattern.
type entityState struct {
	Number int
	Fields [entityFieldCount]int32
}

// playerState stores the raw networked state so later delta snapshots can be
// decoded with the same source data the client uses.
type playerState struct {
	Fields     [playerStateFieldCount]int32
	Stats      [maxStats]int32
	Persistant [maxPersistant]int32
	Holdable   [maxHoldable]int32
	Powerups   [maxPowerups]int32
	Ammo       [maxAmmoGroups * ammoPerGroup]int32
	AmmoClip   [maxAmmoGroups * ammoPerGroup]int32
}

type snapshotState struct {
	Valid            bool
	MessageNum       int
	DeltaNum         int
	ServerTime       int
	ParseEntitiesNum int
	NumEntities      int
	PlayerState      playerState
}
