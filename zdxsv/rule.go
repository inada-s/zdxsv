package main

import (
	"bytes"
	"encoding/binary"
)

const (
	rulePlayerCount   = 0
	ruleBattleCount   = 1
	ruleVisibleTeam   = 2
	ruleBattleTime    = 3
	ruleBattleGauge   = 4
	ruleReloadType    = 5
	ruleDamageLevel   = 6
	ruleAwakeType     = 7
	ruleAwakeIncRatio = 8
	ruleAwakeDecRatio = 9
)

var ruleList []ruleElement
var defaultRule Rule

func init() {
	setupRuleList()
	setupDefaultRule()
}

type ruleElementElement struct {
	Name  string
	Value uint16
}

type ruleElement struct {
	Title   string
	Default byte
	List    []ruleElementElement
}

type Rule struct {
	playerCount   uint16
	battleCount   byte
	visibleTeam   byte
	battleTime    uint16
	battleGauge   uint16
	reloadType    byte
	damageLevel   byte
	awakeType     byte
	awakeIncRatio uint16
	awakeDecRatio uint16

	selected map[byte]byte
}

func NewRule() *Rule {
	r := defaultRule
	r.selected = make(map[byte]byte)
	return &r
}

// Serialize is the 0x6914 battle rule of the game.
func (r *Rule) Serialize(game string) []byte {
	if game == gameCodeAEUG {
		return r.serializeAEUG()
	}
	return r.serializeZGVSZ()
}

// serializeAEUG: a 32-byte MS mask, then the shared fields. The game reads
// six u16 at +0x42..+0x4c and uses its own default for 0.
func (r *Rule) serializeAEUG() []byte {
	const bodyLen = 0x4e
	b := new(bytes.Buffer)
	binary.Write(b, binary.BigEndian, uint16(bodyLen))
	b.Write([]byte{0, 0, 0, 0}) // CPU difficulty, empty slots, stage mode, stage ID
	b.Write(bytes.Repeat([]byte{0xFF}, 32))
	r.writeShared(b)
	b.Write(make([]byte, 2+bodyLen-b.Len()))
	return b.Bytes()
}

func (r *Rule) writeShared(b *bytes.Buffer) {
	var zero8 byte
	binary.Write(b, binary.BigEndian, zero8) // MS/MA Auto Only
	binary.Write(b, binary.BigEndian, r.visibleTeam)
	binary.Write(b, binary.BigEndian, r.battleGauge)
	binary.Write(b, binary.BigEndian, r.battleGauge)
	binary.Write(b, binary.BigEndian, r.awakeIncRatio)
	binary.Write(b, binary.BigEndian, r.awakeDecRatio)
	binary.Write(b, binary.BigEndian, r.battleTime)
	binary.Write(b, binary.BigEndian, zero8) // Nonzero: boost gauge does not drain; only in a game mode a lobby battle is not in
	binary.Write(b, binary.BigEndian, r.reloadType)
	binary.Write(b, binary.BigEndian, zero8) // Nonzero: radar off
	binary.Write(b, binary.BigEndian, zero8) // Nonzero: button-triggered voice messages off
	binary.Write(b, binary.BigEndian, r.damageLevel-1)
	binary.Write(b, binary.BigEndian, r.battleCount)
}

func (r *Rule) serializeZGVSZ() []byte {
	b := new(bytes.Buffer)
	var zero8 byte
	var (
		// Shop points a battle earns, in tenths: own score x pointScore, then
		// +500 x pointWin or -500 x pointLose; clamped to 0-100000.
		pointScore byte
		pointWin   byte
		pointLose  byte
		zzPilot    byte = 1
	)
	binary.Write(b, binary.BigEndian, uint16(100)) // Rule Length
	binary.Write(b, binary.BigEndian, zero8)       // CPU difficulty, 0-7
	binary.Write(b, binary.BigEndian, zero8)       // Empty slots: 0xff = no CPUs, other values fill them with CPUs
	binary.Write(b, binary.BigEndian, zero8)       // Stage mode: 0 = random without space stages, 2 = stage ID below, 3/4 = random
	binary.Write(b, binary.BigEndian, zero8)       // Stage ID, used by stage mode 2
	for i := 0; i < 16*4; i++ {
		binary.Write(b, binary.BigEndian, byte(0xFF)) // Available MS Mask
	}
	r.writeShared(b)
	binary.Write(b, binary.BigEndian, uint64(0)) // Stored by the game, never read
	binary.Write(b, binary.BigEndian, r.awakeType)
	binary.Write(b, binary.BigEndian, zero8) // Weapon Visible
	binary.Write(b, binary.BigEndian, pointScore)
	binary.Write(b, binary.BigEndian, pointWin)
	binary.Write(b, binary.BigEndian, pointLose)
	binary.Write(b, binary.BigEndian, zzPilot)
	return b.Bytes()
}

func validRule(ruleID byte) bool {
	return int(ruleID) < len(ruleList)
}

func validElement(ruleID, elemID byte) bool {
	return validRule(ruleID) && int(elemID) < len(ruleList[ruleID].List)
}

// Set selects the element of the rule; an unknown rule or element is ignored.
func (r *Rule) Set(ruleID, elemID byte) {
	if !validElement(ruleID, elemID) {
		return
	}
	r.selected[ruleID] = byte(elemID)
	value := ruleList[ruleID].List[elemID].Value
	switch ruleID {
	case rulePlayerCount:
		r.playerCount = value
	case ruleBattleCount:
		r.battleCount = byte(value)
	case ruleVisibleTeam:
		r.visibleTeam = byte(value)
	case ruleBattleTime:
		r.battleTime = value
	case ruleBattleGauge:
		r.battleGauge = value
	case ruleReloadType:
		r.reloadType = byte(value)
	case ruleDamageLevel:
		r.damageLevel = byte(value)
	case ruleAwakeType:
		r.awakeType = byte(value)
	case ruleAwakeIncRatio:
		r.awakeIncRatio = value
	case ruleAwakeDecRatio:
		r.awakeDecRatio = value
	}
}

func (r *Rule) Get(ruleID byte) byte {
	v, ok := r.selected[ruleID]
	if ok {
		return v
	}
	if !validRule(ruleID) {
		return 0
	}
	return ruleList[ruleID].Default
}

// rulesOf is the room menu of the game: SLPS-25305 has no awakening.
func rulesOf(game string) []ruleElement {
	if game == gameCodeAEUG {
		return ruleList[:ruleAwakeType]
	}
	return ruleList
}

func RuleCount(game string) byte {
	return byte(len(rulesOf(game)))
}

func RuleTitle(game string, ruleID byte) string {
	if int(ruleID) >= len(rulesOf(game)) {
		return ""
	}
	return ruleList[ruleID].Title
}

func RuleElementCount(game string, ruleID byte) byte {
	if int(ruleID) >= len(rulesOf(game)) {
		return 0
	}
	return byte(len(ruleList[ruleID].List))
}

func RuleElementName(game string, ruleID, elemID byte) string {
	if int(ruleID) >= len(rulesOf(game)) || !validElement(ruleID, elemID) {
		return ""
	}
	return ruleList[ruleID].List[elemID].Name
}

func RuleElementDefault(ruleID byte) byte {
	if !validRule(ruleID) {
		return 0
	}
	return ruleList[ruleID].Default
}

func setupRuleList() {
	ruleList = []ruleElement{
		{"人数設定", 2, []ruleElementElement{
			{"２人", 2}, {"３人", 3}, {"４人", 4}}},
		{"連続対戦数", 0, []ruleElementElement{
			{"任意", 0}, {"１戦", 1}, {"３戦", 3}, {"５戦", 5}, {"１０戦", 10}}},
		{"選択可視範囲", 0, []ruleElementElement{
			{"全員オープン", 0}, {"味方のみ", 1}, {"自分のみ", 2}}},
		{"作戦時間", 3, []ruleElementElement{
			{"９０秒", 90}, {"１２０秒", 120}, {"１８０秒", 180}, {"２１０秒", 210}, {"２７０秒", 270}}},
		{"戦力ゲージ", 2, []ruleElementElement{
			{"１", 1}, {"３００", 300}, {"６００", 600}, {"６１０", 610}, {"６２５", 625}, {"９００", 900}, {"１２００", 1200}}},
		{"リロード制限", 0, []ruleElementElement{
			{"通常", 0}, {"回復なし", 1}, {"制限なし", 2}}},
		{"ダメージレベル", 2, []ruleElementElement{
			{"１", 1}, {"２", 2}, {"３", 3}, {"４", 4}}},
		{"覚醒システム", 0, []ruleElementElement{
			{"任意", 0}, {"\"強襲\"固定", 1}, {"\"復活\"固定", 2}, {"\"機動\"固定", 3}, {"覚醒なし", 4}}},
		{"覚醒ゲージ増加量 ", 2, []ruleElementElement{
			{"１０％", 10}, {"５０％", 50}, {"１００％", 100}, {"１５０％", 150}, {"２００％", 200}}},
		{"覚醒継続時間", 2, []ruleElementElement{
			{"１０％", 10}, {"５０％", 50}, {"１００％", 100}, {"１５０％", 150}, {"２００％", 200}}},
	}
}

func setupDefaultRule() {
	defaultRule = Rule{
		playerCount:   ruleList[rulePlayerCount].List[ruleList[rulePlayerCount].Default].Value,
		battleCount:   byte(ruleList[ruleBattleCount].List[ruleList[ruleBattleCount].Default].Value),
		visibleTeam:   byte(ruleList[ruleVisibleTeam].List[ruleList[ruleVisibleTeam].Default].Value),
		battleTime:    ruleList[ruleBattleTime].List[ruleList[ruleBattleTime].Default].Value,
		battleGauge:   ruleList[ruleBattleGauge].List[ruleList[ruleBattleGauge].Default].Value,
		reloadType:    byte(ruleList[ruleReloadType].List[ruleList[ruleReloadType].Default].Value),
		damageLevel:   byte(ruleList[ruleDamageLevel].List[ruleList[ruleDamageLevel].Default].Value),
		awakeType:     byte(ruleList[ruleAwakeType].List[ruleList[ruleAwakeType].Default].Value),
		awakeIncRatio: ruleList[ruleAwakeIncRatio].List[ruleList[ruleAwakeIncRatio].Default].Value,
		awakeDecRatio: ruleList[ruleAwakeDecRatio].List[ruleList[ruleAwakeDecRatio].Default].Value,
	}
}

// RuleKeys are the rule names a lobby setting may fix.
var RuleKeys = map[string]byte{
	"battle_count":    ruleBattleCount,
	"visible_team":    ruleVisibleTeam,
	"battle_time":     ruleBattleTime,
	"battle_gauge":    ruleBattleGauge,
	"reload_type":     ruleReloadType,
	"damage_level":    ruleDamageLevel,
	"awake_type":      ruleAwakeType,
	"awake_inc_ratio": ruleAwakeIncRatio,
	"awake_dec_ratio": ruleAwakeDecRatio,
}

// SetByKey sets the rule named key (RuleKeys) to v, in the room menu or not; false when the key is
// unknown or v does not fit the rule's field.
func (r *Rule) SetByKey(key string, v uint16) bool {
	id, ok := RuleKeys[key]
	if !ok {
		return false
	}
	switch id {
	case ruleBattleTime:
		r.battleTime = v
		return true
	case ruleBattleGauge:
		r.battleGauge = v
		return true
	case ruleAwakeIncRatio:
		r.awakeIncRatio = v
		return true
	case ruleAwakeDecRatio:
		r.awakeDecRatio = v
		return true
	}
	if 0xff < v {
		return false
	}
	switch id {
	case ruleBattleCount:
		r.battleCount = byte(v)
	case ruleVisibleTeam:
		r.visibleTeam = byte(v)
	case ruleReloadType:
		r.reloadType = byte(v)
	case ruleDamageLevel:
		// Serialize sends the level minus 1.
		if v == 0 {
			return false
		}
		r.damageLevel = byte(v)
	case ruleAwakeType:
		r.awakeType = byte(v)
	}
	return true
}

// RuleDefault is the menu default value of the rule named key (RuleKeys).
func RuleDefault(key string) uint16 {
	id, ok := RuleKeys[key]
	if !ok {
		return 0
	}
	return ruleList[id].List[ruleList[id].Default].Value
}
