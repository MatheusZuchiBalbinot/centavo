# Showcase

As telas do painel do Centavo, capturadas de rodadas reais do laboratório. A
r0012 aparece na página do resultado e no replay, e o sorteio é o que criou a
r0028. A arena ao vivo é da r0031, uma rodada que derruba a API no meio das
transferências e em que também houve ataques feitos à mão. Todas as rodadas
mostradas passaram, com as oito garantias mantidas.

## Início

![A página inicial com o placar do laboratório](inicio.png)

A página inicial convida a tentar fazer o banco perder um centavo. Dá para
escolher um dos seis ataques e a duração, ou apertar "Ataque surpresa". Ao
lado, o cartão do laboratório mostra a sequência atual (23 rodadas seguidas
sem perder um centavo), as operações conferidas nas últimas rodadas e os
recordes, como o de ataques numa rodada aprovada: 45, na r0023, com link para
ela.

## O sorteio

![O sorteio do ataque surpresa](sorteio.png)

O ataque surpresa gira uma roleta com os ataques do cardápio e para em um.
Aqui caiu em "Relógio adiantado e worker zumbi", com 5 minutos, e virou a
r0028. A duração também é sorteada. Antes da contagem de 3 segundos, dá para
deixar um palpite: o banco aguenta ou vai quebrar. O palpite fica guardado no
navegador, e o veredito diz se ele acertou.

## A arena ao vivo

![A arena da r0031 aos 2 minutos](arena-ao-vivo.png)

A rodada abre em tela cheia. O mapa mostra os processos do sistema e as
conexões entre eles, com pontos correndo onde há tráfego. No caminho dos
clientes, cada ponto é uma operação, e a cor diz como ela terminou: azul
concluída, amarelo recusada, roxo sem resposta. Em vermelho fica o que bate
num corte ou num processo caído e volta. A r0031 é do ataque "Derrubar os
workers no meio do PIX" e caiu no cenário que mata a API entre o débito e o
crédito de uma transferência. Aos 2 minutos e 17 segundos, a API está
reiniciando há 17 segundos, depois do ataque 26, uma falha injetada no código.

À direita, o auditor continua em R$ 0,00. Embaixo dele, cada ataque aparece
com a primeira defesa que veio depois e o tempo até o alvo voltar ao normal.
Nas falhas 22 a 26, o worker 2 assumiu o lease do conciliador 9 segundos
depois da primeira.

No pé da tela ficam as ocorrências: cada ataque, cada defesa e cada
transferência é um ponto, com uma cor para cada tipo, e os ataques manuais
têm um anel branco. Na r0031, a linha das transferências tem buracos nos
momentos em que a API caiu. Passar o mouse num ponto, ou focar a faixa e usar
as setas, mostra o que aconteceu ali: o alvo de um ataque, ou as contas, o
valor, o resultado e o tempo de resposta de uma transferência.

## Ataque manual

![O worker 1 logo depois de um ataque manual](ataque-manual.png)

Enquanto dura a fase de ataque, dá para atacar à mão. Um clique num processo
ou numa conexão abre o que aconteceu ali e os botões: derrubar o processo,
congelá-lo por 20 segundos, cortar a conexão por 15 ou deixá-la lenta por 20.
Na imagem, o worker 1 acabou de cair no ataque manual 21. Ele está
reiniciando há 2 segundos, e os botões só voltam quando ele estiver de pé. O
histórico ao lado mostra que ele já tinha caído aos 25 segundos, noutro
ataque manual, e voltado aos 31. A r0031 teve cinco ataques manuais entre os
35 da rodada, e o verificador conferiu tudo como confere as outras rodadas. O
relatório conta esses ataques à parte, porque a semente repete os ataques do
cenário, mas não os feitos à mão.

## O veredito

![O carimbo de aprovação na r0013](veredito.png)

Quando o verificador termina, o veredito cai na arena. A r0013 levou 36
falhas injetadas no meio do código, e o banco respondeu com 34 defesas
registradas: entregas retomadas por outro worker, respostas repetidas tiradas
da chave de idempotência. Os pontos verdes, no pé da tela, são essas
defesas. No ataque 31, uma falha no meio do código do worker 2, o worker 1
retomou 5 PIX 2 segundos depois. Daqui dá para assistir de novo ou abrir a
página da rodada.

## O replay

![O replay da r0012 pausado em 1:44](replay.png)

Qualquer rodada terminada pode ser tocada de novo na arena, a 1, 10, 30 ou 60
vezes a velocidade real, com uma barra para ir a qualquer momento. O link
guarda o momento (`?t=1:44`), então dá para mandar alguém direto para o
ataque que interessa. O replay não tem as leituras do auditor, e o cartão do
dinheiro diz isso em vez de mostrar um valor.

## A página da rodada

![A página da r0012 com o gráfico segundo a segundo](rodada.png)

A página explica a rodada em uma frase e mostra as quatro fases: ataque, cura,
verificação e veredito. Os cartões resumem o dinheiro, as operações por
segundo, o tempo de resposta e as operações recusadas ou sem resposta. No
gráfico, os ataques aparecem numerados no alto e a cura fica sombreada. Ao
lado, o que se esperava da rodada (com todas as defesas ligadas, passar) e o
que aconteceu.

## Garantias e linha do tempo

![As oito garantias e o que aconteceu na rodada](garantias-e-feed.png)

Mais abaixo ficam as oito garantias que o verificador conferiu, cada uma
explicada em uma frase, e a linha do tempo da rodada. Os filtros separam
ataques, defesas e cura. Na r0012, a conciliação corrigiu 178 divergências
com o parceiro, que mentia de propósito.

## Glossário

![A explicação de lease ao passar o mouse](glossario.png)

Termos técnicos aparecem sublinhados. Passar o mouse ou focar com o teclado
abre a explicação e a lista das garantias que aquele mecanismo protege.

## Modo técnico

![A linha do tempo no modo técnico](tecnico.png)

O seletor "Como mostrar" troca a linguagem do painel. No modo técnico, as
garantias ganham os códigos do verificador (I1 a I8) e cada evento mostra o
nome do failpoint que disparou, como `recon.after_compute_before_apply`, e o
registro cru do histórico.

## Modo Apresentação

![A vitrine tocando a r0031 em replay](vitrine.png)

No modo Apresentação, a rodada ao vivo abre direto na arena. Com o laboratório
livre, a página ao vivo vira uma vitrine que toca a rodada aprovada mais
recente em loop, a 30 vezes a velocidade real. Esse modo nunca começa uma
rodada sozinho.

## No celular

<p>
  <img src="celular.png" width="49%" alt="A página da r0012 numa tela de celular">
  <img src="celular-arena.png" width="49%" alt="A arena da r0012 numa tela de celular">
</p>

O painel funciona numa tela de 390 pixels. No celular, o mapa da arena gira e
passa a correr de cima para baixo.
