package replay

// The texts this package writes for people, in the lab's one language: every
// report, panel and timeline text lives here and nowhere else.
const (
	eventKilled        = "processo morto: "
	eventFailpoint     = "failpoint %s (%s) em %s"
	eventFrozen        = "contêiner congelado: %s por %s"
	eventDatabase      = "banco de dados: "
	eventClock         = "relógio desviado em %s: %s"
	eventPartnerLies   = "o parceiro passa a mentir: "
	eventHealStarted   = "cura começou"
	eventHealCompleted = "cura terminou"
	eventNetwork       = "rede: %s em %v"
	eventFailover      = "failover: réplica promovida"
	eventProcessState  = "%s: %s"
	eventDefense       = "defesa em %s: %s (%d)"
)
