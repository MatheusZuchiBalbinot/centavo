# Centavo

[![CI](https://github.com/MatheusZuchiBalbinot/centavo/actions/workflows/ci.yml/badge.svg)](https://github.com/MatheusZuchiBalbinot/centavo/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Um banco digital pequeno, feito para ser atacado. Clientes robôs depositam,
transferem e mandam PIX sem parar enquanto um laboratório de caos derruba
processos, corta a rede, faz o banco parceiro mentir e troca o banco de dados
de líder. No fim de cada rodada, um verificador confere o histórico inteiro e
diz se algum centavo sumiu, apareceu ou foi lançado duas vezes.

![A arena de uma rodada ao vivo](showcase/arena-ao-vivo.png)

- [Showcase](showcase/README.md): as telas do painel, com o que cada uma mostra.
- [Como funciona](docs/README.md): o banco, os ataques, as defesas, o
  verificador e como o projeto foi feito.

## Rodar

Precisa de Docker com Compose v2.

```sh
git clone https://github.com/MatheusZuchiBalbinot/centavo.git
cd centavo
./entrypoint.sh
```

O `entrypoint.sh` confere o Docker e a porta 8090, gera um `.env` com senhas
aleatórias, constrói as imagens e espera todos os serviços ficarem saudáveis.
A primeira vez leva alguns minutos. Depois, abra <http://localhost:8090> e
comece uma rodada.

Para parar, `docker compose down`. Os dados ficam no volume e voltam na
próxima subida.

## Licença

MIT.
