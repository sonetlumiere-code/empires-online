# ADR-014: La deduplicación de comandos es de corto plazo; los efectos durables son idempotentes por construcción

Propósito: fijar qué garantiza la idempotencia de los comandos cliente→servidor, dónde vive y qué se exige
a un comando cuyo efecto durable no se pueda repetir sin daño. Sustituye la parte de
[ADR-004](ADR-004-redis-hot-state.md) que hacía de la tabla `idempotency_keys` «la garantía».

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-10-20** |
| Ámbito | `internal/websocket`, `internal/persistence/redis`, `migrations/` |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-004](ADR-004-redis-hot-state.md), [ADR-006](ADR-006-websocket-protocol.md), [ADR-013](ADR-013-persistence-pipeline.md) |

---

## 1. Contexto

Todo comando cliente→servidor lleva un `requestId` (UUIDv4). El servidor lo reclama en Redis,
`idem:{playerId}:{requestId}` con TTL de 300 s, y un `requestId` repetido dentro de esa ventana no vuelve a
ejecutarse ([INV-SEC-007](../invariants/security.md#inv-sec-007)). Es lo que impide que un reintento tras
una reconexión produzca un segundo movimiento.

[ADR-004](ADR-004-redis-hot-state.md) añadía que, para los comandos con efectos durables, el registro iría
**también** a la tabla `idempotency_keys` de PostgreSQL, que sería «la garantía». Esa mitad nunca se
implementó: la tabla existe desde la migración `000001` y ninguna ruta la escribe.

Implementarla tal como estaba descrita choca con dos reglas que el resto del sistema ya cumple:

- La comprobación tiene que ocurrir **antes** de aplicar el comando, en el borde. Consultar PostgreSQL ahí
  pone una ida y vuelta a la base de datos en la ruta de cada comando, que ADR-004 descartó por coste.
- Escribir el registro **después**, en la cola de persistencia, no protege nada: el efecto ya se aplicó en
  RAM, y la escritura del registro y la del efecto van en trabajos distintos que no forman una transacción.

Lo que realmente se repite cuando un cliente reintenta pasados 300 s es poco: hoy los únicos comandos con
efecto son `unit.move` y `unit.cancel_move`, y repetir una orden de movimiento hacia el mismo destino, o
repetir una cancelación, deja el mundo igual. Comandos como entrenar unidades o construir edificios
(fuera del MVP) sí tendrían efectos que no se pueden repetir sin daño.

---

## 2. Decisión

**La deduplicación de comandos es de corto plazo y vive sólo en Redis; un comando cuyo efecto durable no se
pueda repetir sin daño debe ser idempotente por construcción, y la tabla `idempotency_keys` se elimina.**

1. `idem:{playerId}:{requestId}` con TTL de 300 s sigue siendo el mecanismo, sin cambios. Cubre la ventana
   en la que un cliente reintenta de verdad: reconexiones y reenvíos tras un corte.
2. Si Redis no responde, el comando se ejecuta igualmente, como hasta ahora
   ([../architecture/networking.md](../architecture/networking.md)): se prefiere no bloquear el juego.
3. **Regla para los comandos futuros.** Un comando cuyo efecto durable no se pueda aplicar dos veces sin daño
   —crear unidades, gastar recursos, construir— persiste su `requestId` **en la misma fila y la misma
   transacción que su efecto**, con una restricción `UNIQUE`. La segunda ejecución choca con la restricción
   y no produce efecto. La idempotencia queda así atada al dato que protege, en lugar de depender de un
   registro aparte que puede desincronizarse.
4. La migración `000004_drop_idempotency_keys` elimina la tabla. Su `.down.sql` la vuelve a crear tal como
   estaba.

---

## 3. Alternativas consideradas

### 3.1 Implementar `idempotency_keys` como decía ADR-004

**Ventajas reales.** Una única tabla protege cualquier comando, sin pensar en cada uno, y sobrevive a un
`FLUSHALL` de Redis y a reintentos tardíos.

**Descarte.** O la comprobación consulta PostgreSQL en la ruta de cada comando, que es el coste que ADR-004
ya rechazó, o el registro se escribe después del efecto en un trabajo separado, y entonces no forma una
transacción con él: un fallo entre ambos deja el efecto sin registro, o el registro sin efecto. La garantía
sería más débil que la de la regla 3 y bastante más cara.

### 3.2 Conservar la tabla vacía «por si acaso»

**Ventajas reales.** Cero trabajo, y el esquema queda listo si algún día se quiere la alternativa 3.1.

**Descarte.** Una tabla que la documentación presenta como garantía y que nadie escribe es exactamente el
tipo de promesa falsa que `EO-118` ha tenido que deshacer documento por documento. Si algún día hace falta,
una migración la crea en minutos.

---

## 4. Consecuencias

### Positivas

- La documentación deja de prometer una garantía que no existe.
- Cada comando futuro con efectos no repetibles tiene una regla clara y barata, que además es atómica con su
  efecto.
- Desaparece una tabla del esquema y de los procedimientos de copia y restauración.

### Negativas

- Un reintento **más allá de 300 s**, o tras un `FLUSHALL` de Redis, vuelve a ejecutar el comando. Con los
  comandos actuales el resultado es el mismo mundo; con los futuros, la regla 3 es obligatoria y hay que
  recordarla al diseñarlos.
- Con Redis caído no hay deduplicación ninguna durante la caída. Ya era así.

### Neutras

- La migración `000004` cambia la versión del esquema.

---

## 5. Verificación

- `TestComandoDuplicadoSeIgnora` (`internal/websocket`) y `TestIdempotenciaDeduplicaComandos`
  (`internal/persistence/redis`) siguen siendo la verificación de la ventana de 300 s.
- Tras migrar, `idempotency_keys` no existe, y `down` la recrea.

## 6. Referencias

- [ADR-004](ADR-004-redis-hot-state.md), cuya garantía durable de idempotencia queda sustituida por ésta.
- [../invariants/security.md](../invariants/security.md), `INV-SEC-007`.
- [../invariants/persistence.md](../invariants/persistence.md), `INV-PERSIST-003`.
