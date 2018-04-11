package main

import (
	"bufio"
	"encoding/hex"
	"io"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"sync"

	"container/heap"
	"flag"
	"fmt"
	"strings"

	"net/http"

	"github.com/PuerkitoBio/goquery"
)

const (
	// ENDMESSAGE - кодовая фраза на случай конца потока:
	ENDMESSAGE = "stop"
	// URLY - Yandex url
	URLY = "https://yandex.ru"
)

var (
	WORKERS    = 5     //количество рабочих
	WORKERSCAP = 5     //размер очереди каждого рабочего
	IMGDIR     = "img" //директория для сохранения картинок
)

//Назначим флаги командной строки:
func init() {
	flag.IntVar(&WORKERS, "w", WORKERS, "количество рабочих")
	flag.StringVar(&IMGDIR, "d", IMGDIR, "директория для картинок")
}

// MakeRequestURL create search-url based on entered word
func MakeRequestURL(word string) string {
	binary := []byte(word)
	encodedStr := strings.ToUpper(hex.EncodeToString(binary)) // string like D0A1D0B1...
	re := regexp.MustCompile("..")                            // make regex expression
	slice := re.FindAllString(encodedStr, -1)                 // string split like [D0 A1 D0 B1 ...]
	encodedStr = strings.Join(slice, "%")                     // now the string is D0%A1%D0%B1%...
	requestURL := URLY + "/images/search?text=%" + encodedStr
	return requestURL
}

// Генератор загружает страницу и достает из нее ссылки на картинки
func generator(out chan string, search string, number int) error {
	doc, err := goquery.NewDocument(MakeRequestURL(search))
	if err != nil {
		return fmt.Errorf("сan't open the website, please check your Internet connection")
	}
	idx := 0
	doc.Find("a").EachWithBreak(func(i int, s *goquery.Selection) bool {
		if class, ok := s.Attr("class"); ok {
			if class == "serp-item__link" {
				u, _ := s.Attr("href")
				idx++
				image, _ := goquery.NewDocument(URLY + u)
				// fmt.Println(image)
				image.Find("div").EachWithBreak(func(j int, sel *goquery.Selection) bool {
					cl, _ := sel.Attr("class")

					// ПРОБЛЕМА В ЭТОМ МЕСТЕ
					// ЕСЛИ НАЖАТЬ НА КАРТИНКУ БУДЕТ ВИДНО, КАК В HTML ЕСТЬ
					// src указывающий на непосредственную картинку, которую и надо скачать
					// но вот при запуске атрибут src в HTML отсутствует
					// -> его надо вытаскивать по-другому!

					if cl == "preview2__thumb-wrapper" {
						fmt.Println(i, cl)
						sel.Children().EachWithBreak(func(k int, kid *goquery.Selection) bool {
							link, o := kid.Attr("src")
							fmt.Println("src: ", link, "\twe have this image - ", o)
							out <- link
							return false
						})
						return false
					}
					// link, _ := sel.Attr("src")
					// fmt.Println(i, j, link)
					// jpg := strings.Contains(link, ".jpg")
					// jpeg := strings.Contains(link, ".jpeg")
					// png := strings.Contains(link, ".png")
					// // fmt.Println(i, j, link)
					// // target, _ := sel.Attr("target")
					// // tabindex, _ := sel.Attr("tabindex")
					// // rel, _ := sel.Attr("rel")
					// // <img class="preview2__thumb preview2__thumb_overlay_yes preview2__thumb_fit_height" alt="" src="http://images6.fanpop.com/image/photos/38600000/Batman-batman-38689074-1600-900.jpg" style="max-width: 1600px; max-height: 900px;">
					// if jpg || jpeg || png {
					// 	// img, e := sel.Attr("href")
					// 	fmt.Println(link)
					// 	out <- link
					// 	return false
					// }
					// return false
					return true
				})
				if idx == number {
					out <- ENDMESSAGE
					return false
				}
			}
		}
		return true
	})
	// doc, err := goquery.ParseUrl(MakeRequestURL(search))
	// if err != nil {
	// 	return fmt.Errorf("сan't open the website, please check your Internet connection")
	// }
	// for pos := 1; ; pos++ {
	// 	//Разбираем страницу:
	// 	// if err == nil {
	// 	//Отправляем все найденные ссылки в поток:
	// 	doc.
	// 	for p, u := range doc.Find("a").Attrs("href") {
	// 		fmt.Println("->", URLY+u)
	// 		out <- URLY + u
	// 	}
	// 	fmt.Println(pos)
	// 	//А если встретили признак последней страницы - отправляем кодовую фразу..
	// 	if pos == number {
	// 		out <- ENDMESSAGE
	// 		fmt.Println("stop")
	// 		//..и прекращаем работу генератора
	// 		return nil
	// 	}
	// 	// }
	// }
	return nil
}

// Worker рабочий
type Worker struct {
	urls    chan string     // канал для заданий
	pending int             // кол-во оставшихся задач
	index   int             // позиция в куче
	wg      *sync.WaitGroup //указатель на группу ожидания
}

//В качестве аргумента получаем указатель на канал завершения
func (w *Worker) work(done chan *Worker) {
	for {
		u := <-w.urls //читаем следующее задание
		w.wg.Add(1)   //инкриминируем счетчик группы ожидания
		// fmt.Println("-> ", u)
		download(u) //загружаем файл
		w.wg.Done() //сигнализируем группе ожидания что закончили
		done <- w   //показываем что завершили работу
	}
}

// Pool Это будет наша "куча":
type Pool []*Worker

//Проверка кто меньше - в нашем случае меньше тот у кого меньше заданий:
func (p Pool) Less(i, j int) bool { return p[i].pending < p[j].pending }

//Вернем количество рабочих в пуле:
func (p Pool) Len() int { return len(p) }

//Реализуем обмен местами:
func (p Pool) Swap(i, j int) {
	if i >= 0 && i < len(p) && j >= 0 && j < len(p) {
		p[i], p[j] = p[j], p[i]
		p[i].index, p[j].index = i, j
	}
}

//Push - Заталкивание элемента:
func (p *Pool) Push(x interface{}) {
	n := len(*p)
	worker := x.(*Worker)
	worker.index = n
	*p = append(*p, worker)
}

//Pop - выталкивание:
func (p *Pool) Pop() interface{} {
	old := *p
	n := len(old)
	item := old[n-1]
	item.index = -1
	*p = old[0 : n-1]
	return item
}

//Balancer - Балансировщик
type Balancer struct {
	pool     Pool            //Наша "куча" рабочих
	done     chan *Worker    //Канал уведомления о завершении для рабочих
	requests chan string     //Канал для получения новых заданий
	flowctrl chan bool       //Канал для PMFC
	queue    int             //Количество незавершенных заданий переданных рабочим
	wg       *sync.WaitGroup //Группа ожидания для рабочих
}

//Инициализируем балансировщик. Аргументом получаем канал по которому приходят задания
func (b *Balancer) init(in chan string) {
	b.requests = make(chan string)
	b.flowctrl = make(chan bool)
	b.done = make(chan *Worker)
	b.wg = new(sync.WaitGroup)

	//Запускаем наш Flow Control:
	go func() {
		for {
			b.requests <- <-in //получаем новое задание и пересылаем его на внутренний канал
			<-b.flowctrl       //а потом ждем получения подтверждения
		}
	}()

	//Инициализируем кучу и создаем рабочих:
	heap.Init(&b.pool)
	for i := 0; i < WORKERS; i++ {
		w := &Worker{
			urls:    make(chan string, WORKERSCAP),
			index:   0,
			pending: 0,
			wg:      b.wg,
		}
		go w.work(b.done)     //запускаем рабочего
		heap.Push(&b.pool, w) //и заталкиваем его в кучу
	}
}

//Рабочая функция балансировщика получает аргументом канал уведомлений от главного цикла
func (b *Balancer) balance(quit chan bool) {
	lastjobs := false //Флаг завершения, поднимаем когда кончились задания
	for {
		select { //В цикле ожидаем коммуникации по каналам:

		case <-quit: //пришло указание на остановку работы
			b.wg.Wait()  //ждем завершения текущих загрузок рабочими..
			quit <- true //..и отправляем сигнал что закончили

		case u := <-b.requests: //Получено новое задание (от flow controller)
			if u != ENDMESSAGE { //Проверяем - а не кодовая ли это фраза?
				b.dispatch(u) // если нет, то отправляем рабочим
			} else {
				fmt.Println(ENDMESSAGE)
				lastjobs = true //иначе поднимаем флаг завершения
			}

		case w := <-b.done: //пришло уведомление, что рабочий закончил загрузку
			b.completed(w) //обновляем его данные
			if lastjobs {
				if w.pending == 0 { //если у рабочего кончились задания..
					heap.Remove(&b.pool, w.index) //то удаляем его из кучи
				}
				if len(b.pool) == 0 { //а если куча стала пуста
					//значит все рабочие закончили свои очереди
					quit <- true //и можно отправлять сигнал подтверждения готовности к останову
				}
			}
		}
	}
}

// Функция отправки задания
func (b *Balancer) dispatch(url string) {
	w := heap.Pop(&b.pool).(*Worker) //Берем из кучи самого незагруженного рабочего..
	w.urls <- url                    //..и отправляем ему задание.
	w.pending++                      //Добавляем ему "весу"..
	heap.Push(&b.pool, w)            //..и отправляем назад в кучу
	if b.queue++; b.queue < WORKERS*WORKERSCAP {
		b.flowctrl <- true
	}
}

//Обработка завершения задания
func (b *Balancer) completed(w *Worker) {
	w.pending--
	heap.Remove(&b.pool, w.index)
	heap.Push(&b.pool, w)
	if b.queue--; b.queue == WORKERS*WORKERSCAP-1 {
		b.flowctrl <- true
	}
}

// Downloading the image
func download(url string) {
	fileName := IMGDIR + "/" + url[strings.LastIndex(url, "/")+1:]
	output, err := os.Create(fileName)
	defer output.Close()

	resp, err := http.Get(url)
	if err != nil {
		fmt.Println("Error while downloading", url, "-", err)
		return
	}
	defer resp.Body.Close()
	io.Copy(output, resp.Body)
}

func main() {
	//разберем флаги
	flag.Parse()
	//создадим директорию для загрузки, если её еще нет
	if err := os.MkdirAll(IMGDIR, 666); err != nil {
		panic(err)
	}

	//Подготовим каналы и балансировщик
	links := make(chan string)
	quit := make(chan bool)
	b := new(Balancer)
	b.init(links)

	//Приготовимся перехватывать сигнал останова в канал keys
	keys := make(chan os.Signal, 1)
	signal.Notify(keys, os.Interrupt)
	fmt.Println("search")
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	search := scanner.Text()
	fmt.Println("N")
	scanner.Scan()
	N, err := strconv.Atoi(scanner.Text())
	if err != nil {
		panic(err)
	}

	//Запускаем балансировщик и генератор
	go b.balance(quit)
	go generator(links, search, N)

	fmt.Println("Начинаем загрузку изображений...")
	//Основной цикл программы:
	for {
		select {
		case <-keys: //пришла информация от нотификатора сигналов:
			fmt.Println("CTRL-C: Ожидаю завершения активных загрузок")
			quit <- true //посылаем сигнал останова балансировщику

		case <-quit: //пришло подтверждение о завершении от балансировщика
			fmt.Println("Загрузки завершены!")
			return
		}
	}
}
