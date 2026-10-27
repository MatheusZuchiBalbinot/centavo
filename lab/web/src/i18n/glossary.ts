export type TermId =
	| 'lease'
	| 'fencing'
	| 'idempotency'
	| 'idempotencyKey'
	| 'failpoint'
	| 'doubleEntry'
	| 'saga'
	| 'outbox'
	| 'inbox'
	| 'webhook'
	| 'reconciliation'
	| 'failover'
	| 'replica'
	| 'toxic'
	| 'cure'
	| 'checker'
	| 'auditor';

export type GlossaryEntry = {
	readonly term: string;
	readonly text: string;
	// Guarantee ids; empty for lab vocabulary.
	readonly protects: readonly string[];
	// Lowercase, as the word appears in the panel's texts.
	readonly forms: readonly string[];
};

export const glossary: Readonly<Record<TermId, GlossaryEntry>> = {
	lease: {
		term: 'Lease',
		text: 'Uma reserva com prazo: só quem segura o lease de uma transferência pode trabalhar nela. Se o worker morre, o prazo vence e outro assume.',
		protects: ['I2', 'I5'],
		forms: ['leases', 'lease'],
	},
	fencing: {
		term: 'Fencing',
		text: 'Cada lease carrega um número que só cresce. O banco recusa a gravação de quem segura um número velho, como um worker que acordou depois de congelado.',
		protects: ['I1', 'I2'],
		forms: ['fencing'],
	},
	idempotency: {
		term: 'Idempotência',
		text: 'Fazer a mesma operação duas vezes tem o efeito de fazer uma. É o que deixa o cliente repetir um PIX sem medo quando a resposta se perde.',
		protects: ['I2', 'I3'],
		forms: ['idempotência', 'idempotente'],
	},
	idempotencyKey: {
		term: 'Chave de idempotência',
		text: 'Um id que o cliente manda junto com a escrita. Se a mesma chave chega de novo, o banco devolve a resposta guardada em vez de debitar outra vez.',
		protects: ['I2', 'I3'],
		forms: ['chave de idempotência', 'mesma chave'],
	},
	failpoint: {
		term: 'Failpoint',
		text: 'Um ponto marcado no código do banco onde o laboratório pode fazer o processo morrer, congelar ou errar, exatamente entre dois passos.',
		protects: [],
		forms: ['failpoint', 'falha injetada no meio do código'],
	},
	doubleEntry: {
		term: 'Partida dobrada',
		text: 'Todo dinheiro que sai de uma conta entra em outra no mesmo lançamento. Por isso a soma de todos os saldos é sempre zero.',
		protects: ['I1'],
		forms: ['partida dobrada'],
	},
	saga: {
		term: 'Saga',
		text: 'Um PIX para outro banco é uma sequência de passos (reservar, enviar, confirmar ou devolver). A saga garante que ele sempre chegue ao fim, mesmo com falhas no caminho.',
		protects: ['I5', 'I7'],
		forms: ['sagas', 'saga'],
	},
	outbox: {
		term: 'Outbox',
		text: 'O banco grava o PIX e o aviso de envio na mesma transação. Assim nunca fica um PIX gravado sem aviso, nem um aviso sem PIX.',
		protects: ['I5'],
		forms: ['outbox'],
	},
	inbox: {
		term: 'Inbox',
		text: 'O banco anota cada webhook que recebe pelo id. Um webhook repetido é reconhecido e não aplica o crédito de novo.',
		protects: ['I2', 'I6'],
		forms: ['inbox'],
	},
	webhook: {
		term: 'Webhook',
		text: 'O aviso que o parceiro manda de volta ao banco: o PIX foi liquidado, devolvido ou chegou. Ele pode se perder, atrasar ou chegar duas vezes.',
		protects: ['I6'],
		forms: ['webhooks', 'webhook'],
	},
	reconciliation: {
		term: 'Conciliação',
		text: 'De tempos em tempos o banco compara o seu registro com o extrato do parceiro e corrige o que divergir, como um webhook que nunca chegou.',
		protects: ['I6'],
		forms: ['conciliação', 'conciliam', 'concilia'],
	},
	failover: {
		term: 'Failover',
		text: 'Quando o Postgres principal cai, uma réplica é promovida a líder e o banco passa a gravar nela.',
		protects: ['I1', 'I8'],
		forms: ['failover', 'trocando de líder'],
	},
	replica: {
		term: 'Réplica',
		text: 'Uma cópia do Postgres que recebe cada gravação do principal e pode assumir o lugar dele.',
		protects: ['I8'],
		forms: ['réplica'],
	},
	toxic: {
		term: 'Toxic',
		text: 'Uma falha de rede que o laboratório injeta num link: latência, banda baixa, conexão cortada ou resposta que nunca volta.',
		protects: [],
		forms: ['toxic'],
	},
	cure: {
		term: 'Cura',
		text: 'Depois dos ataques, o laboratório desliga todas as falhas e espera o banco terminar o que ficou pendente, antes de conferir.',
		protects: [],
		forms: ['a cura', 'cura'],
	},
	checker: {
		term: 'Verificador',
		text: 'Um programa à parte que lê o histórico de tudo o que os clientes viram e o banco inteiro, e confere as oito garantias no fim da rodada.',
		protects: [],
		forms: ['verificador'],
	},
	auditor: {
		term: 'Auditor',
		text: 'Um leitor do banco de fora, sem passar pelo código dele, que soma todos os saldos a cada 2 segundos enquanto a rodada acontece.',
		protects: ['I1', 'I4'],
		forms: ['auditor'],
	},
};
