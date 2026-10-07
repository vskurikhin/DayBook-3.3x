package tracelog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// traceQueueCapacity определяет максимальную ёмкость очереди сообщений трассировки.
	// Ограничение задаётся по количеству сообщений, а не по суммарному объёму занимаемой памяти.
	traceQueueCapacity = 4096

	// traceTimeLayout — формат метки события: локальное время с
	// микросекундами; микросекунды усекаются, часовой пояс не меняется.
	traceTimeLayout = "2006/01/02 15:04:05.000000"
)

// Prefix — префикс трассировки: буква состояния, ID узла и терм.
// Буква состояния предоставляется вызывающим (в Raft — из состояния модуля консенсуса).
// Скаляры снимаются при приёме сообщения; форматирование выполняет писатель.
type Prefix struct {
	// Letter — буква состояния узла; неизвестное состояние — «?».
	Letter rune

	// ID — идентификатор узла.
	ID int

	// Term — текущий терм узла.
	Term int
}

// FormatPrefix — единственный источник формата обязательного префикса
// строки трассировки: буква состояния, идентификатор узла и терм.
// Используется и писателем, и синхронным путём вывода вызывающего
// (стандартный поток).
func FormatPrefix(p Prefix) string {
	return fmt.Sprintf("[%c,N:%d,T:%03d] ", p.Letter, p.ID, p.Term)
}

// messageKind — вид элемента очереди трассировки.
type messageKind uint8

const (
	// messageData — строка трассировки: тело либо готово (ready),
	// либо формируется из format и args при выводе.
	messageData messageKind = iota

	// messageFlush — маркер границы: ответ получает первая ошибка
	// записи, накопленная к моменту обработки маркера; в файл не пишется.
	messageFlush

	// messageStop — маркер остановки писателя: обрабатывается после
	// всех ранее принятых сообщений; в файл не пишется.
	messageStop
)

// message — сообщение очереди трассировки. Префиксные скаляры сняты
// вызывающим под его блокировкой в момент приёма, метка времени берётся
// часами писателя; вызывающий передаёт либо format и args, либо готовое
// тело.
type message struct {
	kind   messageKind
	prefix Prefix
	at     time.Time
	format string
	args   []any
	text   string
	ready  bool
	reply  chan error
}

// Writer — самостоятельный писатель трассировки: владеет очередью,
// сериализацией приёма и вывода, первой ошибкой записи и состоянием
// завершения. Тип не читает глобальные переменные и состояние
// вызывающего: приёмник, ёмкость очереди и часы внедряются конструктором.
//
// Иерархия блокировок вызывающего: его блокировка состояния (в Raft —
// cm.mu) → admit → writeMu → errMu. Горутина писателя берёт только
// writeMu → errMu.
type Writer struct {
	// writer — приёмник полных строк; закрывает его владелец.
	writer io.Writer

	// clock — источник метки события; вызывается при приёме сообщения.
	clock func() time.Time

	// ch — очередь сообщений; не закрывается никогда.
	ch chan message

	// admit — двоичный семафор сериализации приёма и смены режима:
	// канал ёмкости 1 с токеном, который возвращает взявший.
	admit chan struct{}

	// writeMu сериализует вывод: горутину писателя, резервный путь
	// переполнения и синхронные постановки после остановки.
	writeMu sync.Mutex

	// wake — coalescing-сигнал писателю о новых сообщениях, ёмкость 1;
	// остаточный сигнал допустим.
	wake chan struct{}

	// done закрывается при выходе горутины, после освобождения writeMu.
	done chan struct{}

	// errMu защищает первую ошибку записи; из-под него другие
	// блокировки не берутся.
	errMu sync.Mutex
	err   error

	// syncMode переключается при принятом stop-маркере: последующие
	// постановки выполняются синхронно под writeMu.
	syncMode atomic.Bool
}

// New создаёт писателя с одной горутиной и возвращает экземпляр только
// после её сигнала готовности. Ёмкость очереди должна быть положительной,
// приёмник и часы — не nil: нарушение контракта является программной
// ошибкой вызывающего.
func New(writer io.Writer, capacity int, clock func() time.Time) *Writer {
	if writer == nil || capacity <= 0 || clock == nil {
		panic("raft: trace writer requires a writer, a positive capacity and a clock")
	}
	w := &Writer{
		writer: writer,
		clock:  clock,
		ch:     make(chan message, capacity),
		admit:  make(chan struct{}, 1),
		wake:   make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
	ready := make(chan struct{})
	go w.run(ready)
	<-ready
	return w
}

// run — цикл горутины писателя: после сигнала готовности ждёт
// пробуждение, берёт writeMu и только после захвата неблокирующе
// забирает сообщения до пустой очереди или stop-маркера. Ожидание
// данных из пустого канала под writeMu не выполняется.
func (w *Writer) run(ready chan<- struct{}) {
	close(ready)
	for {
		<-w.wake
		w.writeMu.Lock()
		for {
			msg, ok := w.dequeue()
			if !ok {
				break
			}
			stop := w.deliver(&msg)
			msg = message{} // отпустить ссылки до ожидания
			if stop {
				w.writeMu.Unlock()
				close(w.done)
				return
			}
		}
		w.writeMu.Unlock()
	}
}

// Enqueue ставит в очередь сообщение, тело которого форматируется
// писателем в момент вывода из format и args.
//
// Контракт владения аргументами: передавать только безопасные значения
// (скаляры, значения без ссылок); они не должны мутироваться с момента
// постановки до завершения обработки именно этого сообщения. Граница
// доставки — успешное завершение покрывающей границы: успешный Flush
// подтверждает обработку сообщений, поставленных до его маркера,
// успешный Shutdown — сообщений до stop-маркера, синхронная постановка
// после остановки — своим возвратом. Возврат из Enqueue означает приём,
// а не обработку; возврат ошибки контекста из отменённого Flush/Shutdown
// аргументы не освобождает: маркер мог быть уже поставлен и сообщение
// всё равно будет обработано писателем (при отмене до постановки маркера
// его может и не быть). Мутация аргументов после отменённого ожидания
// запрещена до подтверждённой покрывающим успешным Flush/Shutdown
// обработки. Изменяемые ссылочные данные сюда передавать нельзя — для
// них есть EnqueueText, а тело формирует вызывающий.
//
//nolint:goprintffuncname // имя закреплено интерфейсом пакета, формат передаётся писателю.
func (w *Writer) Enqueue(p Prefix, format string, args ...any) {
	w.enqueue(&message{kind: messageData, prefix: p, format: format, args: args})
}

// EnqueueText ставит в очередь сообщение с готовым телом: строка уже
// сформирована вызывающим и далее не мутируется. Это единственный путь
// для изменяемых ссылочных данных — их чтение после постановки
// исключается, в очереди хранится только строка. Граница доставки и
// контракт мутации — как у Enqueue.
func (w *Writer) EnqueueText(p Prefix, text string) {
	w.enqueue(&message{kind: messageData, prefix: p, text: text, ready: true})
}

// enqueue принимает сообщение отправителя, удерживающего свою блокировку
// состояния. Приём и смена режима сериализованы admit; метка времени
// берётся часами после входа в admit. В синхронном режиме постановка
// ждёт done и пишет под writeMu, чтобы не обогнать хвост. При полной
// очереди отправитель, не выпуская admit, переходит на резервный путь
// с опустошением очереди.
func (w *Writer) enqueue(msg *message) {
	w.admit <- struct{}{}
	defer func() { <-w.admit }()
	msg.at = w.clock()
	if w.syncMode.Load() {
		<-w.done
		w.writeMu.Lock()
		defer w.writeMu.Unlock()
		w.deliver(msg)
		return
	}
	select {
	case w.ch <- *msg:
		w.signalWake()
	default:
		w.reserve(msg)
	}
}

// reserve — синхронный резервный путь переполнения: под writeMu сначала
// записывается вся ранее принятая очередь, затем своя строка. Stop-маркер
// здесь встретиться не может: его постановка и перевод в синхронный
// режим выполняются под одним токеном admit до любой следующей постановки.
func (w *Writer) reserve(msg *message) {
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	for {
		queued, ok := w.dequeue()
		if !ok {
			break
		}
		w.deliver(&queued)
		queued = message{} // отпустить ссылки до следующего шага
	}
	w.deliver(msg)
}

// Flush ставит в очередь Flush-маркер и ждёт его обработки или отмены
// контекста. Линеаризация успеха — постановка маркера; отмена после неё
// маркер не удаляет. В асинхронном режиме admit освобождается до ожидания
// ответа, чтобы другие отправители не ждали границу; в синхронном режиме
// ожидание done выполняется под admit, чтобы граница не пересекалась с
// новой постановкой.
func (w *Writer) Flush(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return w.joinErr(err)
	}
	if err := w.acquireAdmit(ctx); err != nil {
		return err
	}
	if w.syncMode.Load() {
		// Синхронный режим: все предыдущие синхронные постановки уже
		// завершены; ожидание done выполняется под admit, чтобы граница
		// Flush не пересекалась с новой постановкой.
		err := w.waitDone(ctx)
		w.releaseAdmit()
		return err
	}
	reply := make(chan error, 1)
	placeErr := w.place(ctx, &message{kind: messageFlush, reply: reply})
	w.releaseAdmit()
	if placeErr != nil {
		return placeErr
	}
	select {
	case err := <-reply:
		return err
	case <-ctx.Done():
		return w.joinErr(ctx.Err())
	}
}

// Shutdown останавливает горутину писателя: под admit ставит единственный
// stop-маркер, затем переводит режим в синхронный. Маркер ставится по
// состоянию режима, а не по памяти о предыдущем вызове; повторный вызов
// новых маркеров не создаёт и ждёт тот же done. Принятый stop-маркер не
// откатывается даже при отмене ожидания.
func (w *Writer) Shutdown(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return w.joinErr(err)
	}
	if err := w.acquireAdmit(ctx); err != nil {
		return err
	}
	if w.syncMode.Load() {
		w.releaseAdmit()
		return w.waitDone(ctx)
	}
	if err := w.place(ctx, &message{kind: messageStop}); err != nil {
		w.releaseAdmit()
		return err
	}
	w.syncMode.Store(true)
	w.releaseAdmit()
	return w.waitDone(ctx)
}

// place ставит сообщение или маркер в очередь, ожидая свободного места
// с выбором отмены контекста. Требует удержания admit.
func (w *Writer) place(ctx context.Context, msg *message) error {
	select {
	case w.ch <- *msg:
		w.signalWake()
		return nil
	case <-ctx.Done():
		return w.joinErr(ctx.Err())
	}
}

// acquireAdmit берёт токен admit или прекращает ожидание по контексту.
func (w *Writer) acquireAdmit(ctx context.Context) error {
	select {
	case w.admit <- struct{}{}:
		return nil
	case <-ctx.Done():
		return w.joinErr(ctx.Err())
	}
}

// releaseAdmit возвращает токен admit.
func (w *Writer) releaseAdmit() { <-w.admit }

// waitDone ждёт остановки горутины писателя или отмены контекста и
// возвращает первую ошибку записи.
func (w *Writer) waitDone(ctx context.Context) error {
	select {
	case <-w.done:
		return w.stickyError()
	case <-ctx.Done():
		return w.joinErr(ctx.Err())
	}
}

// signalWake неблокирующе будит горутину писателя.
func (w *Writer) signalWake() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// dequeue неблокирующе забирает сообщение из очереди.
func (w *Writer) dequeue() (message, bool) {
	select {
	case msg := <-w.ch:
		return msg, true
	default:
		return message{}, false
	}
}

// deliver обрабатывает одно сообщение: пишет строку данных, отвечает на
// Flush-маркер первой ошибкой записи или сообщает о stop-маркере.
// Возвращает true только для stop-маркера.
func (w *Writer) deliver(msg *message) bool {
	switch msg.kind {
	case messageFlush:
		msg.reply <- w.stickyError()
	case messageStop:
		return true
	case messageData:
		w.writeLine(msg)
	}
	return false
}

// writeLine формирует и записывает полную строку одним вызовом Write:
// метка события, префикс и тело. Перевод строки добавляется только при
// его отсутствии. Короткая запись считается ошибкой.
func (w *Writer) writeLine(msg *message) {
	body := msg.text
	if !msg.ready {
		body = fmt.Sprintf(msg.format, msg.args...)
	}
	line := msg.at.Format(traceTimeLayout) + " " + FormatPrefix(msg.prefix) + body
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}
	n, err := io.WriteString(w.writer, line)
	if err == nil && n < len(line) {
		err = io.ErrShortWrite
	}
	w.recordError(err)
}

// recordError сохраняет первую ошибку записи; последующие её не заменяют.
func (w *Writer) recordError(err error) {
	if err == nil {
		return
	}
	w.errMu.Lock()
	defer w.errMu.Unlock()
	if w.err == nil {
		w.err = err
	}
}

// RecordError сохраняет первую ошибку записи от внешнего источника —
// синхронного вывода вызывающего вне очереди (например, в стандартный
// поток). Последующие ошибки первую не заменяют.
func (w *Writer) RecordError(err error) {
	w.recordError(err)
}

// stickyError возвращает первую сохранённую ошибку записи.
func (w *Writer) stickyError() error {
	w.errMu.Lock()
	defer w.errMu.Unlock()
	return w.err
}

// joinErr объединяет ошибку контекста с уже известной ошибкой записи.
func (w *Writer) joinErr(ctxErr error) error {
	if err := w.stickyError(); err != nil {
		return errors.Join(ctxErr, err)
	}
	return ctxErr
}
