package domain

import "golang.org/x/sys/unix"

// Кольцевой буфер даёт O(1) добавление/удаление и не требует realloc/append как у обычного slice.
type RingBuffer struct {
	data []byte // фиксированное хранилище байт (ёмкость буфера = len(data))

	r int // read index: индекс, откуда мы будем читать следующий байт
	w int // write index: индекс, куда мы будем писать следующий байт

	n int // bytes currently stored
}

// Буфер фиксированного размера
func NewRingBuffer(capacity int) *RingBuffer {
	if capacity <= 0 {
		panic("ring buffer capacity must be > 0")
	}
	return &RingBuffer{data: make([]byte, capacity)}
}

// Cap — общая ёмкость буфера (в байтах).
func (b *RingBuffer) Cap() int { return len(b.data) }

// Len — сколько байт сейчас хранится в буфере.
func (b *RingBuffer) Len() int { return b.n }

func (b *RingBuffer) Empty() bool { return b.n == 0 }

func (b *RingBuffer) Full() bool { return b.n == len(b.data) }

// Readable — сколько байт доступно для чтения
func (b *RingBuffer) Readable() int { return b.n }

// Writable — сколько байт можно записать, не переполняя буфер.
func (b *RingBuffer) Writable() int { return len(b.data) - b.n }

// ReadFromFD читает из fd в буфер до тех пор, пока:
// - в сокете есть данные (read возвращает >0),
// - и в буфере есть место,
// - и пока read не вернёт EAGAIN/EWOULDBLOCK.
//
// Возвращает:
// bytesRead: сколько байт реально прочитали и положили в буфер
// gotEOF: true если получили EOF => peer закрыл запись
func (b *RingBuffer) ReadFromFD(fd int) (int, bool, error) {
	// Если буфер уже полон — читать некуда.
	if b.Full() {
		return 0, false, nil
	}

	total := 0

	// Читаем пока есть место в буфере.
	for !b.Full() {
		// Получаем "срезы" свободного места для записи без копирования:
		// из-за кольцевой структуры свободное место может быть в конце массива (p1)
		// и, если данные "обернулись", ещё в начале массива (p2).
		p1, p2 := b.writeSlices()

		// Если есть первый непрерывный кусок свободного места.
		if len(p1) > 0 {
			n, err := unix.Read(fd, p1)
			if n > 0 {
				// Продвигаем write index и увеличиваем счётчик заполненности.
				b.advanceWrite(n)
				total += n
			}

			if err != nil {
				// EAGAIN/EWOULDBLOCK означает: данных больше нет прямо сейчас.
				if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
					return total, false, nil
				}
				return total, false, err
			}

			// n==0 означает EOF: peer закрыл запись.
			if n == 0 {
				return total, true, nil
			}

			continue
		}

		// Если p1 пустой, значит свободное место (если оно есть) только во втором сегменте p2.
		n, err := unix.Read(fd, p2)
		if n > 0 {
			b.advanceWrite(n)
			total += n
		}

		if err != nil {
			if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
				return total, false, nil
			}
			return total, false, err
		}

		if n == 0 {
			return total, true, nil
		}

	}

	// Буфер заполнился — больше читать нельзя.
	return total, false, nil
}

// WriteToFD пишет данные из буфера в fd до тех пор, пока:
// в буфере есть данные,
// и сокет принимает запись,
// и пока write не вернёт EAGAIN/EWOULDBLOCK.
// Возвращает:
// - bytesWritten: сколько байт реально отправили в сокет
func (b *RingBuffer) WriteToFD(fd int) (int, error) {
	// Если нечего писать — ок.
	if b.Empty() {
		return 0, nil
	}

	total := 0

	// Пишем пока в буфере есть данные.
	for !b.Empty() {
		// Получаем "срезы" данных:
		// если данные непрерывны — p1,
		// если "обёрнуты" — p1 в конце массива и p2 в начале.
		p1, p2 := b.readSlices()

		// Пытаемся писать сначала из p1.
		if len(p1) > 0 {
			n, err := unix.Write(fd, p1)
			if n > 0 {
				// Сдвигаем read index и уменьшаем заполненность.
				b.advanceRead(n)
				total += n
			}

			if err != nil {
				// EAGAIN/EWOULDBLOCK означает: сокет сейчас не принимает.
				if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
					return total, nil
				}
				return total, err
			}

			if n < len(p1) {
				return total, nil
			}

			// p1 полностью записали — пробуем дальше.
			continue
		}

		// Если p1 пуст, пишем из p2.
		n, err := unix.Write(fd, p2)
		if n > 0 {
			b.advanceRead(n)
			total += n
		}

		if err != nil {
			if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
				return total, nil
			}
			return total, err
		}

		if n < len(p2) {
			return total, nil
		}
	}

	return total, nil
}

// PeekRead возвращает до двух слайсов данных, доступных для чтения
func (b *RingBuffer) PeekRead() ([]byte, []byte) { return b.readSlices() }

// Consume "съедает" n байт из начала буфера
// Используется для протокольного парсинга: разобрали N байт handshake -> убрали их из буфера.
func (b *RingBuffer) Consume(n int) {
	if n <= 0 {
		return
	}

	// Если съели всё или больше — просто сбрасываем индексы в ноль.
	if n >= b.n {
		b.r, b.w, b.n = 0, 0, 0
		return
	}

	// Иначе продвигаем read index на n байт.
	b.advanceRead(n)
}

// ---- internal helpers ----

// readSlices возвращает данные для чтения как один или два слайса.
// 1) Если r < w: данные непрерывно лежат в data[r:w].
// 2) Если r >= w: данные "обёрнуты":
// первый сегмент: data[r:] до конца массива,
// второй сегмент: data[:k] в начале массива.
func (b *RingBuffer) readSlices() ([]byte, []byte) {
	if b.n == 0 {
		return nil, nil
	}

	if b.r < b.w {
		return b.data[b.r:b.w], nil
	}

	p1 := b.data[b.r:]
	// Иногда n меньше длины "хвоста",
	// тогда весь readable диапазон помещается в p1.
	if len(p1) > b.n {
		p1 = p1[:b.n]
		return p1, nil
	}

	// Остаток данных лежит в начале массива.
	p2len := b.n - len(p1)
	return p1, b.data[:p2len]
}

// writeSlices возвращает свободное место для записи как один или два слайса.
func (b *RingBuffer) writeSlices() ([]byte, []byte) {
	if b.n == len(b.data) {
		return nil, nil
	}

	// Если write указатель "позади" read указателя — свободное место непрерывное [w:r).
	if b.w < b.r {
		return b.data[b.w:b.r], nil
	}

	// Иначе свободное место может быть:
	//до конца массива от w,
	// и возможно в начале массива до r.
	space := len(b.data) - b.n

	// Первый сегмент свободного места — от w до конца массива.
	p1 := b.data[b.w:]
	if len(p1) > space {
		// Свободного места меньше, чем осталось до конца массива.
		p1 = p1[:space]
		return p1, nil
	}

	// Сколько свободного места осталось после p1.
	space2 := space - len(p1)
	if space2 == 0 {
		return p1, nil
	}

	// Второй сегмент не может заходить за r, иначе затрём непрочитанные данные.
	if space2 > b.r {
		space2 = b.r
	}

	return p1, b.data[:space2]
}

// advanceWrite сдвигает write index на n байт
// и увеличивает количество хранимых байт.
func (b *RingBuffer) advanceWrite(n int) {
	b.w = (b.w + n) % len(b.data)
	b.n += n
}

// advanceRead сдвигает read index на n байт и уменьшает количество хранимых байт.
func (b *RingBuffer) advanceRead(n int) {
	b.r = (b.r + n) % len(b.data)
	b.n -= n
}

// WriteBytes кладёт данные p в буфер, с копированием в data.
// Пишет не больше свободного места (bounded). Возвращает, сколько байт реально записали.
// Используется для мелких сообщений (например ответ на greeting), которые нужно поставить в очередь.
func (b *RingBuffer) WriteBytes(p []byte) int {
	if len(p) == 0 || b.Full() {
		return 0
	}

	written := 0

	// Копируем порциями
	for written < len(p) && !b.Full() {
		p1, p2 := b.writeSlices()

		if len(p1) > 0 {
			n := copy(p1, p[written:])
			b.advanceWrite(n)
			written += n
			continue
		}

		if len(p2) > 0 {
			n := copy(p2, p[written:])
			b.advanceWrite(n)
			written += n
			continue
		}

		break
	}

	return written
}
