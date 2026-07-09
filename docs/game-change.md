# O que "game change" significa aqui — documento de decisão

> Rascunho vivo, iniciado em 2026-07-09. Não é um roadmap de features; é a tese
> do produto e as decisões que decorrem dela. Roadmap operacional continua no
> memory `product_roadmap`.

## A tese em uma frase

KodeKloud e Killer.sh são **catálogos fixos**: alguém escolheu os labs, para as
certificações que eles decidiram cobrir. Este produto **gera o exercício exato
para a lacuna exata do aluno, valida num cluster real, e reforça na hora certa —
para qualquer certificação**. O diferencial não é "tem IA"; é fechar um loop de
aprendizado que catálogo nenhum fecha.

## O que é game change e o que é hype (sendo honesto)

**Não é game change:** "gera labs com IA". Gerar conteúdo é commodity e é o lado
_frágil_ (o modelo pequeno alucina — o bug do `SOLUTION:` que vazava o gabarito
foi exatamente isso). Se o produto se vender como "gerador de labs", ele compete
na dimensão em que é mais fraco que o KodeKloud.

**É game change:** o **loop fechado** abaixo. Cada peça isolada já existe no
mercado; ninguém junta as cinco para K8s/cert, e menos ainda a custo ~zero com IA
local por usuário.

```
  detectar a lacuna exata   →   gerar o exercício sob medida
  (skill tracker, EWMA)          (generator + grounding + preflight)
          ↑                                   ↓
  reforçar no momento certo   ←   validar no cluster real + explicar o erro
  (spaced repetition)             (goals compilados + LLMExplainFailure)
```

Correção honesta após ler o código (o rascunho anterior errou o diagnóstico):
**quatro das cinco setas já existiam**. O scheduler de spaced repetition estava
lá — `ReviewItem` com intervalo que dobra no acerto e reseta na falha,
`ReviewQueue`, e `GenerateReplayLab` que transforma revisão vencida em lab. O que
faltava era mais preciso:

1. **Mastery gating** — não existia mesmo. O aluno avançava quando queria;
   `GenerateLearningPath` cuspia todos os tópicos de uma vez. **[FEITO 2026-07-09]**
   `mastery.go`: a trilha só libera o próximo tópico quando o atual é dominado
   (score ≥ 0.75, ≥ 3 tentativas, **e** sem revisão vencida — o que amarra
   "passar" a "reter"). Travados aparecem com cadeado mas não viram lab.
2. **Reforço era pull-only** — o scheduler existia, mas o nudge proativo
   (`Advise`) era _cego_ à fila de revisão: o aluno só via reforço se digitasse
   "revisão". **[FEITO 2026-07-09]** `Advise` agora traz as revisões vencidas
   primeiro. A seta virou push, não pull.

## As três decisões que travam tudo

### Decisão 1 — Métrica: passar E reter (cravado pelo dono, 2026-07-09)

Não é "cobrir os objetivos". É **passar na prova e ainda saber daqui a 3 meses**.
Consequência de engenharia: toda feature se justifica por mover uma destas duas
agulhas, e elas exigem coisas diferentes —

- _passar_ → mastery learning, realismo de prova, cobertura dos domínios/pesos.
- _reter_ → spaced repetition, active recall, dificuldade desejável (esconder o
  gabarito é isto — já fazemos, e o bug do vazamento estava _destruindo_ esse
  efeito silenciosamente).

### Decisão 2 — Confiança do conteúdo: separar gerado de curado

O maior risco do produto (memory `product_roadmap`, lacuna nº1). **Regra:** IA
gera só onde erro é barato — dica, explicação de erro, personalização, primeira
versão de um lab que **passa pelo preflight + auto-verificação antes de chegar ao
aluno**. Os objetivos oficiais de cada cert têm um núcleo curado/verificado. O
aluno vê o selo: "verificado" vs "gerado por IA, com citação da fonte".

Isto _não contradiz_ a tese da geração sob demanda. A geração é o moat; a
verificação é o que a torna confiável o bastante para uma prova de cert. Sem o
gate, a geração é passivo, não ativo.

### Decisão 3 — Todo texto de LLM que chega ao aluno passa por um guard

Prompt não é contrato. Modelo pequeno desobedece. A partir de agora:

- **saída → aluno** passa por sanitização (`RedactSolutionCommands`, `HideLabSpoilers`).
- **saída estruturada** (quiz, plano) valida item-a-item; item ruim é descartado,
  nunca mata o batch.
- **a regra distingue diagnóstico de solução**: mandar o aluno rodar
  `kubectl get/describe/logs` é tutoria; entregar `kubectl apply` é vazar o
  gabarito. O guard esconde o segundo, preserva o primeiro.

## O que construir primeiro (ordem por ROI de aprendizado)

1. ~~**Scheduler de reforço**~~ — já existia; o que faltava (push proativo via
   `Advise`) foi feito em 2026-07-09.
2. ~~**Mastery gate**~~ — feito em 2026-07-09 (`mastery.go`). **Próximo passo
   natural:** quando o gate trava um tópico, disparar automaticamente a geração
   do lab da fronteira (hoje o gate _classifica_ e a trilha _gera os liberados_;
   falta o "gerou sob medida a lacuna exata" ser automático no dashboard, não só
   no chat).
3. **Selo curado vs gerado + gate de qualidade** — nenhum lab gerado chega ao
   aluno sem passar auto-verificação; UI marca a procedência com citação. **É o
   maior risco aberto** (confiança de conteúdo) — provavelmente o próximo a
   atacar.
4. **Frente de UI** — o backend do mastery/reforço existe e está no
   `/api/tutor/status` (`mastery`, `review`, `recommendations`); falta a UI da
   trilha desenhar os cadeados e o "N revisões vencidas hoje".
5. **Modo prova cronometrado por domínios/pesos** — vira "Killer.sh
   self-hostado". Código de currículo (`CurriculumFor`/domains) já existe.

## Como saber se deu certo (senão é hype)

- Aprovação: taxa de acerto no modo prova por domínio, antes/depois da trilha.
- Retenção: acerto num tópico **reapresentado** após N dias (o scheduler dá esse
  dado de graça).
- Confiança: % de conteúdo gerado que passa no gate de primeira; quantos
  gabaritos vazam (meta: zero — regressão coberta por teste).

## Anti-metas (para não virar mais um KodeKloud pior)

- Não perseguir catálogo gigante hand-made — é a força _deles_, nosso custo.
- Não adicionar feature que não mova aprovação ou retenção.
- Não confiar em saída de LLM sem gate — foi o que quase entregou a resposta
  pronta ao aluno.
