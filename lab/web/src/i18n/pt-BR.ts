// Every text the panel shows, in its one language. Components read them by
// key; a text with {placeholders} goes through format().
export const messages = {
  appName: 'Centavo',
  skipToContent: 'Pular para o conteúdo',
  labNotice: 'Falhas injetadas de propósito, num banco que não move dinheiro de verdade.',
  nav: {
    label: 'Seções',
    home: 'Início',
  },
  labStatus: {
    idle: 'Laboratório livre',
    running: 'Rodada em andamento',
    stopping: 'Parando a rodada',
    unknown: 'Lendo o laboratório',
    offline: 'Laboratório fora do ar',
  },
  titles: {
    home: 'Centavo: tente fazer o banco perder um centavo',
    notFound: 'Página não encontrada · Centavo',
  },
  home: {
    headline: 'Tente fazer o banco perder um centavo.',
    lead: 'Escolha um ataque e aperte começar. Um robô dispara milhares de PIX enquanto o laboratório derruba processos, corta a rede e faz o parceiro mentir. No fim, um verificador confere cada centavo.',
  },
  notFound: {
    headline: 'Esta página não existe.',
    back: 'Voltar para o início',
  },
} as const;
