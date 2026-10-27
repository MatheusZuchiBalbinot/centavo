# Centavo

Um banco pequeno feito para ser atacado. Robôs fazem PIX sem parar enquanto um laboratório derruba processos, corta a rede, faz o parceiro mentir e troca o banco de dados de líder. No fim, um verificador confere o histórico inteiro e diz se algum centavo sumiu, apareceu ou foi duplicado.

## Rodar

Precisa só de Docker com Compose v2.

```sh
./entrypoint.sh
```

O script confere o Docker, gera um `.env` com senhas aleatórias, constrói as imagens e espera tudo ficar saudável. A primeira vez leva alguns minutos. Depois, abra o painel em <http://localhost:8090>, escolha um ataque (ou aperte **Ataque surpresa**) e acompanhe a rodada.

Para parar: `docker compose down`. Os dados ficam no volume e voltam na próxima vez.

## Desenvolver

O código é Go (a versão está nos `go.mod` e é baixada sozinha) e o painel é React, em `lab/web`.

| Comando | O que faz |
| --- | --- |
| `make test` | testes dos três módulos, com e sem o código de caos |
| `make lint` | golangci-lint |
| `make fmt` | formata o código |
| `make check` | confere que o binário de produção não leva código de caos |

## Estrutura

- `bank/`: o banco (API, workers e PostgreSQL).
- `partner/`: o banco parceiro que liquida os PIX, e que às vezes mente.
- `lab/`: o laboratório de caos, o verificador e o painel.

## Licença

MIT.
