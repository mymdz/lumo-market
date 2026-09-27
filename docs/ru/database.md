# Схема базы данных

[English version](../en/database.md) · [Подробное описание](overview.md) · [README](../../README.md)

Описание всех таблиц и колонок, которые пишет симулятор Lumo Market.

- `shop` — бизнес-данные маркетплейса (то, что читают CDC / lakehouse / BI).
- `sim` — служебное состояние симулятора (нужно для рестартов, в аналитике не используется).

Исходники: базовые таблицы в `internal/adapters/postgres/schema.sql`, FK и индексы
в `constraints.sql`, поздние колонки в `internal/app/migrations.go`.

## Общие соглашения

- **Время.** Все `timestamptz` в UTC. Единственное исключение: `returns.received_at`
  (`timestamp` без зоны, местное время склада). Ни одна событийная колонка не бывает в
  будущем; плановыми (могут быть в будущем) являются только `shipments.promised_at`,
  `purchase_orders.expected_at`, `campaigns.starts_at/ends_at`, `coupons.valid_from/valid_to`.
- **Деньги.** `numeric(12,2)`. Цены офферов и закупок в EUR, суммы заказов, платежей,
  возвратов в валюте заказа (`orders.currency`).
- **ID.** Генерируются симулятором (не `serial`), монотонно растут внутри таблицы.
- **FK.** Создаются после первичного backfill как `NOT VALID`: для новых строк
  проверяются, для истории нет. У `orders.cart_id` и `reviews.customer_id` FK нет
  намеренно (корзины удаляются через 30 дней, отзывы переживают GDPR-удаление).
- **Колонки миграций** (помечены ⓜ): появляются в симулированный момент времени,
  строки, созданные до миграции, содержат `NULL`.
- **`updated_at`.** Меняется при каждом UPDATE строки, кроме «окон багов» при
  `dirt_level > 0` (см. README): там строки обновляются без изменения `updated_at`.
- Колонки со значениями-перечислениями хранятся как `text`; ниже перечислены все
  значения, которые может записать симулятор.

## Схема `shop`

### Справочники

#### `shop.currencies` — валюты

| Колонка | Тип | Описание |
|---|---|---|
| `code` | char(3) PK | ISO 4217: `EUR`, `PLN`, `CZK`, `SEK`, `DKK`, `MDL` |
| `name` | text | Название валюты (англ.) |
| `minor_units` | smallint | Число знаков после запятой (всегда 2) |

#### `shop.countries` — страны (рынки)

| Колонка | Тип | Описание |
|---|---|---|
| `code` | char(2) PK | ISO 3166-1 alpha-2 (15 стран: DE, AT, NL, BE, FR, IT, ES, PT, IE, PL, CZ, SE, DK, FI, MD) |
| `name` | text | Название страны (англ.) |
| `currency` | char(3) | Валюта рынка → `currencies.code` |
| `vat_rate` | numeric(5,2) | Стандартная ставка НДС, % |
| `vat_reduced` | numeric(5,2) | Сниженная ставка НДС, % (книги, продукты, детские товары) |
| `timezone` | text | IANA-зона (`Europe/Berlin`), в ней считается суточный профиль спроса |
| `is_eu` | boolean | Член ЕС (`false` только у Молдовы) |
| `launched_at` | timestamptz | Запуск рынка; до этого момента заказов из страны нет |

#### `shop.fx_rates` — курсы валют

Одна строка на день и валюту (кроме EUR).

| Колонка | Тип | Описание |
|---|---|---|
| `rate_date` | date PK | Дата курса |
| `currency` | char(3) PK | Валюта |
| `rate_per_eur` | numeric(18,6) | Сколько единиц валюты за 1 EUR |

#### `shop.warehouses` — склады Lumo (фулфилмент)

| Колонка | Тип | Описание |
|---|---|---|
| `id` | smallint PK | 1 Leipzig, 2 Tilburg, 3 Poznań, 4 Zaragoza |
| `code` | text | Короткий код склада (`LEJ1`, `TLB1`, `POZ1`, `ZAZ1`) |
| `name` | text | Название |
| `country` | char(2) | Страна склада |
| `city` | text | Город |
| `timezone` | text | IANA-зона склада (в ней пишется `returns.received_at`, работает cut-off 14:00) |
| `opened_at` | timestamptz | Открытие; после него сроки доставки в регион падают |

#### `shop.categories` — дерево категорий

Три уровня: департамент (0) → группа (1) → листовая категория (2). Товары висят на листьях.

| Колонка | Тип | Описание |
|---|---|---|
| `id` | integer PK | ID категории |
| `parent_id` | integer | Родитель → `categories.id`; `NULL` у департаментов |
| `name` | text | Название (англ.) |
| `slug` | text | URL-слаг (`smartphones`) |
| `path` | text | Полный путь через ` > ` (`Electronics > Phones & Wearables > Smartphones`) |
| `level` | smallint | Глубина: 0, 1, 2 |
| `is_active` | boolean | Категория активна |
| `created_at` | timestamptz | Создание |
| `updated_at` | timestamptz | Последнее изменение |

#### `shop.brands` — бренды

| Колонка | Тип | Описание |
|---|---|---|
| `id` | integer PK | ID бренда |
| `name` | text | Название |
| `tier` | text | Ценовой сегмент: `budget`, `mid`, `premium` |
| `country` | char(2) | Страна происхождения (может быть вне рынков: CN, US, JP...); `NULL` если неизвестна |
| `is_private_label` | boolean | Собственная марка Lumo |
| `created_at` | timestamptz | Появление бренда в каталоге |

### Маркетплейс и каталог

#### `shop.sellers` — продавцы

| Колонка | Тип | Описание |
|---|---|---|
| `id` | integer PK | ID продавца; `1` — Lumo Retail (сам маркетплейс) |
| `name` | text | Витринное название |
| `legal_name` | text | Юридическое лицо |
| `country` | char(2) | Страна регистрации (в т.ч. CN) |
| `seller_type` | text | `1p` — собственные продажи Lumo, `3p` — сторонний продавец |
| `status` | text | `onboarding` → `active` ⇄ `suspended` → `closed`. У приостановленного продавца офферы уходят в `paused` |
| `rating` | numeric(3,2) | Рейтинг продавца 1–5, пересчитывается по отменам/опозданиям; `NULL` пока нет данных |
| `joined_at` | timestamptz | Регистрация на площадке |
| `updated_at` | timestamptz | Последнее изменение |
| `closed_at` | timestamptz | Уход с площадки (при `status = closed`) |

#### `shop.suppliers` — поставщики

У кого продавцы закупают товар (`purchase_orders`).

| Колонка | Тип | Описание |
|---|---|---|
| `id` | integer PK | ID поставщика |
| `brand_id` | integer | Бренд, если поставщик — дистрибьютор одного бренда; иначе `NULL` |
| `name` | text | Название |
| `country` | char(2) | Страна |
| `lead_time_days` | numeric(5,1) | Средний срок поставки, дней |
| `created_at` | timestamptz | Создание |

#### `shop.products` — товары (карточки)

| Колонка | Тип | Описание |
|---|---|---|
| `id` | bigint PK | ID товара |
| `category_id` | integer | Листовая категория → `categories.id` |
| `brand_id` | integer | Бренд → `brands.id`; `NULL` у no-name товаров (часть — «грязь») |
| `title` | text | Название (при грязи бывают двойные пробелы) |
| `description` | text | Описание; бывает `NULL` или HTML |
| `status` | text | `active` → `discontinued` (снят с производства, распродажа остатков) → `deleted` |
| `weight_g` | integer | Вес, граммы |
| `attributes` | jsonb | Атрибуты уровня товара (`{"tier": "budget"}` и т.п.) |
| `successor_id` | bigint | Следующее поколение товара (`Nova 12 Pro` → `Nova 13 Pro`) → `products.id` |
| `launched_at` | timestamptz | Вывод на рынок |
| `discontinued_at` | timestamptz | Снятие с продажи |
| `created_at` | timestamptz | Создание карточки |
| `updated_at` | timestamptz | Последнее изменение |
| `eco_score` ⓜ | char(1) | Эко-рейтинг `A`–`E`. Миграция `m003` (−310 дней), через 2 месяца массовый backfill ~40% старых товаров |

#### `shop.product_variants` — варианты (SKU)

Размер / цвет / объём памяти конкретного товара.

| Колонка | Тип | Описание |
|---|---|---|
| `id` | bigint PK | ID варианта |
| `product_id` | bigint | Товар → `products.id` |
| `sku` | text | Внутренний артикул (`LM-LFLT-01`) |
| `ean` | char(13) | Штрихкод EAN-13; `NULL` если нет |
| `variant_name` | text | Человекочитаемое имя (`128 GB / Black`); `NULL` у товара без вариантов |
| `attributes` | jsonb | Оси варианта (`{"color_tech": "Black", "storage_phone": "128 GB"}`) |
| `status` | text | `active` |
| `created_at` | timestamptz | Создание |
| `updated_at` | timestamptz | Последнее изменение |

#### `shop.offers` — предложения продавцов

Один вариант может продаваться несколькими продавцами; заказ ссылается на оффер.

| Колонка | Тип | Описание |
|---|---|---|
| `id` | bigint PK | ID оффера |
| `variant_id` | bigint | Вариант → `product_variants.id` |
| `seller_id` | integer | Продавец → `sellers.id` |
| `price` | numeric(12,2) | Текущая цена, EUR (для не-EUR рынков пересчитывается по курсу) |
| `list_price` | numeric(12,2) | «Зачёркнутая» цена (RRP) |
| `currency` | char(3) | Всегда `EUR` |
| `fulfillment` | text | `platform` — хранится на складе Lumo (остатки в `stock_levels`), `seller` — продавец отгружает сам |
| `stock_qty` | integer | Остаток у продавца; только для `fulfillment = seller`, иначе `NULL` |
| `handling_days` | smallint | Дней на сборку у продавца; только для `seller` |
| `status` | text | `active`, `paused` (продавец приостановлен), `deleted` |
| `created_at` | timestamptz | Создание |
| `updated_at` | timestamptz | Последнее изменение (цена, статус, остаток) |

#### `shop.price_history` — история цен офферов

| Колонка | Тип | Описание |
|---|---|---|
| `id` | bigint PK | ID изменения |
| `offer_id` | bigint | Оффер → `offers.id` |
| `old_price` | numeric(12,2) | Цена до, EUR |
| `new_price` | numeric(12,2) | Цена после, EUR |
| `reason` | text | `repricing`, `competitor_match`, `cost_increase`, `campaign_start`, `campaign_end`, `clearance` (распродажа снятого товара), `successor_launch` (вышло новое поколение) |
| `changed_at` | timestamptz | Момент изменения |

### Склад и закупки

#### `shop.stock_levels` — остатки на складах Lumo

Только для офферов с `fulfillment = platform`.

| Колонка | Тип | Описание |
|---|---|---|
| `warehouse_id` | smallint PK | Склад → `warehouses.id` |
| `offer_id` | bigint PK | Оффер → `offers.id` |
| `qty_on_hand` | integer | Физический остаток (при оверселле бывает отрицательным) |
| `qty_reserved` | integer | Зарезервировано под неотгруженные заказы |
| `reorder_point` | integer | Точка перезаказа: ниже неё создаётся закупка |
| `updated_at` | timestamptz | Последнее изменение |

#### `shop.stock_movements` — движения остатков

| Колонка | Тип | Описание |
|---|---|---|
| `id` | bigint PK | ID движения |
| `warehouse_id` | smallint | Склад |
| `offer_id` | bigint | Оффер |
| `qty_delta` | integer | Изменение количества (+ приход, − расход) |
| `reason` | text | `sale` (отгрузка), `return` (возврат на склад), `inbound` (приёмка закупки), `damaged` (списание) |
| `ref_type` | text | Тип документа-основания: `order`, `purchase_order`; `NULL` у списаний |
| `ref_id` | bigint | ID документа-основания |
| `created_at` | timestamptz | Момент движения |

#### `shop.purchase_orders` — закупки у поставщиков

| Колонка | Тип | Описание |
|---|---|---|
| `id` | bigint PK | ID закупки |
| `seller_id` | integer | Кто закупает (обычно Lumo Retail) → `sellers.id` |
| `supplier_id` | integer | Поставщик → `suppliers.id` |
| `warehouse_id` | smallint | Склад приёмки |
| `status` | text | `placed` → `confirmed` → `shipped` → `received`; или `cancelled` |
| `ordered_at` | timestamptz | Размещение |
| `expected_at` | timestamptz | Ожидаемая поставка (**плановая**, может быть в будущем) |
| `received_at` | timestamptz | Фактическая приёмка (бывают срывы сроков) |
| `updated_at` | timestamptz | Последнее изменение |

#### `shop.purchase_order_items` — строки закупки

| Колонка | Тип | Описание |
|---|---|---|
| `id` | bigint PK | ID строки |
| `purchase_order_id` | bigint | Закупка → `purchase_orders.id` |
| `offer_id` | bigint | Оффер → `offers.id` |
| `qty_ordered` | integer | Заказано |
| `qty_received` | integer | Принято (заполняется при `received`, может быть меньше заказанного) |
| `unit_cost` | numeric(12,2) | Закупочная цена за единицу, EUR |

### Клиенты

#### `shop.customers` — покупатели

| Колонка | Тип | Описание |
|---|---|---|
| `id` | bigint PK | ID клиента |
| `email` | text | Email (при грязи: пробелы, заглавные, дубли людей с другим email, тестовые `@lumomarket.test`). После GDPR: `erased-<id>@erased.invalid` |
| `first_name` | text | Имя; `NULL` после GDPR |
| `last_name` | text | Фамилия; `NULL` после GDPR |
| `phone` | text | Телефон в одном из 5 форматов; `NULL` если не указан / после GDPR |
| `birth_date` | date | Дата рождения; может отсутствовать |
| `gender` | text | `female`, `male`, `NULL` |
| `country` | char(2) | Страна регистрации |
| `language` | text | Язык интерфейса (ISO 639-1: `de`, `fr`, `ro`, `ru`...) |
| `marketing_opt_in` | boolean | Согласие на рассылки |
| `loyalty_tier` | text | `none`, `silver`, `gold`, `platinum`; пересчитывается раз в месяц |
| `acquisition_channel` | text | Канал привлечения: `organic`, `paid_search`, `social`, `direct`, `affiliate`, `referral`, `email`, `influencer` |
| `signup_device` | text | `web_desktop`, `web_mobile`, `ios_app`, `android_app` |
| `default_address_id` | bigint | Адрес по умолчанию → `addresses.id` |
| `status` | text | `active`, `blocked` (антифрод), `deleted` (GDPR) |
| `created_at` | timestamptz | Регистрация |
| `updated_at` | timestamptz | Последнее изменение |
| `deleted_at` | timestamptz | Момент GDPR-удаления |
| `phone_verified_at` ⓜ | timestamptz | Подтверждение телефона. Миграция `m005` (+14 дней после первого запуска), затем постепенно раскатывается на старых клиентов |

#### `shop.addresses` — адреса

| Колонка | Тип | Описание |
|---|---|---|
| `id` | bigint PK | ID адреса |
| `customer_id` | bigint | Владелец → `customers.id`; `NULL` у гостевых заказов |
| `recipient_name` | text | Получатель; `[erased]` после GDPR |
| `line1` | text | Улица, дом; `[erased]` после GDPR |
| `line2` | text | Квартира, доп. строка |
| `postal_code` | text | Индекс (грязь: без ведущего нуля, NL без пробела, пустой Eircode) |
| `city` | text | Город (грязь: `München`/`Munich`/`Muenchen`) |
| `country` | char(2) | Страна (грязь: встречается в нижнем регистре) |
| `phone` | text | Телефон для курьера |
| `created_at` | timestamptz | Создание |

### Маркетинг

#### `shop.campaigns` — маркетинговые кампании

Заводятся за 3–6 недель до старта.

| Колонка | Тип | Описание |
|---|---|---|
| `id` | integer PK | ID кампании |
| `name` | text | Название (`Black Friday Week 2025`) |
| `campaign_type` | text | `holiday`, `sale`, `mega_sale`, `season`, `brand_week`, `influencer` |
| `starts_at` | timestamptz | Начало (**плановое**) |
| `ends_at` | timestamptz | Конец (**плановое**) |
| `discount_pct` | numeric(5,2) | Типовая скидка кампании, % |
| `countries` | text[] | Страны действия; `NULL` = все рынки |
| `created_at` | timestamptz | Создание |

#### `shop.coupons` — промокоды

| Колонка | Тип | Описание |
|---|---|---|
| `id` | integer PK | ID купона |
| `code` | text | Промокод (`WELCOME10`, `ANNA15`...) |
| `campaign_id` | integer | Кампания → `campaigns.id`; `NULL` у постоянных кодов |
| `discount_type` | text | `percent` или `fixed` |
| `discount_value` | numeric(10,2) | Размер скидки: % или сумма в EUR |
| `min_order_value` | numeric(10,2) | Минимальная сумма заказа |
| `valid_from` | timestamptz | Начало действия (**плановое**) |
| `valid_to` | timestamptz | Окончание (**плановое**); `NULL` = бессрочно |
| `max_uses` | integer | Лимит использований; `NULL` = без лимита |
| `times_used` | integer | Сколько раз применён |
| `created_at` | timestamptz | Создание |
| `updated_at` | timestamptz | Последнее изменение (растёт вместе с `times_used`) |

### Продажи

#### `shop.carts` — корзины

Корзины старше 30 дней (по `updated_at`) **физически удаляются** ежедневно (hard delete).

| Колонка | Тип | Описание |
|---|---|---|
| `id` | bigint PK | ID корзины |
| `customer_id` | bigint | Клиент; `NULL` у анонимной сессии (может заполниться при логине) |
| `session_id` | uuid | Сессия на сайте / в приложении |
| `status` | text | `active` → `converted` (оформлен заказ) или `abandoned` (брошена; часть возвращается после письма) |
| `country` | char(2) | Рынок сессии |
| `currency` | char(3) | Валюта рынка |
| `converted_order_id` | bigint | Заказ, в который превратилась корзина |
| `created_at` | timestamptz | Создание |
| `updated_at` | timestamptz | Последнее изменение |

#### `shop.cart_items` — позиции корзины

Удаляются вместе с корзиной (`ON DELETE CASCADE`).

| Колонка | Тип | Описание |
|---|---|---|
| `id` | bigint PK | ID позиции |
| `cart_id` | bigint | Корзина → `carts.id` |
| `offer_id` | bigint | Оффер → `offers.id` |
| `qty` | integer | Количество |
| `unit_price` | numeric(12,2) | Цена в момент добавления, в валюте корзины |
| `added_at` | timestamptz | Момент добавления |

#### `shop.orders` — заказы

Все суммы в валюте заказа. `grand_total = items_subtotal − discount_total + shipping_fee`
(налог включён в цены; при грязи редкие расхождения на 1 цент).

| Колонка | Тип | Описание |
|---|---|---|
| `id` | bigint PK | ID заказа |
| `order_number` | text | Номер для клиента, `LM-<год>-<7 цифр>` |
| `customer_id` | bigint | Клиент → `customers.id`; `NULL` у гостевого заказа |
| `guest_email` | text | Email гостя (только при `customer_id IS NULL`) |
| `cart_id` | bigint | Исходная корзина (без FK: корзины удаляются) |
| `status` | text | см. ниже |
| `currency` | char(3) | Валюта заказа = валюта рынка |
| `fx_rate` | numeric(18,6) | Курс на день заказа: единиц валюты за 1 EUR (для EUR = 1) |
| `country` | char(2) | Страна доставки / рынок |
| `items_subtotal` | numeric(12,2) | Сумма позиций до скидки, с НДС |
| `discount_total` | numeric(12,2) | Скидка (купон, кампания) |
| `shipping_fee` | numeric(12,2) | Стоимость доставки (0 при бесплатной) |
| `tax_total` | numeric(12,2) | НДС, включённый в сумму |
| `grand_total` | numeric(12,2) | Итого к оплате |
| `coupon_id` | integer | Применённый купон → `coupons.id` |
| `shipping_address_id` | bigint | Адрес доставки → `addresses.id` |
| `billing_address_id` | bigint | Платёжный адрес |
| `channel` | text | `web_desktop`, `web_mobile`, `ios_app`, `android_app` |
| `shipping_method` | text | `standard`, `express`, `locker` (постамат) |
| `placed_at` | timestamptz | Оформление |
| `paid_at` | timestamptz | Успешная оплата (у наложенного платежа — при вручении) |
| `cancelled_at` | timestamptz | Отмена |
| `cancel_reason` | text | `customer_request`, `payment_failed`, `payment_timeout`, `cod_refused` (отказ от наложенного платежа), `seller_cancelled` |
| `created_at` | timestamptz | Создание строки |
| `updated_at` | timestamptz | Последнее изменение |
| `utm_source` ⓜ | text | `google_organic`, `google_ads`, `instagram`, `tiktok`, `awin`, `referral`, `newsletter`. Миграция `m002` (−470 дней) |
| `utm_campaign` ⓜ | text | Слаг кампании (`black_friday_week`, `brand_week:_klarvo`...). Миграция `m002` |

Статусы заказа: `pending_payment` → `paid` → `processing` → `partially_shipped` →
`shipped` → `delivered` → `partially_returned` / `returned`; из ранних статусов
возможен `cancelled`.

#### `shop.order_items` — позиции заказа

**Нет `updated_at`**: изменения `status` и `returned_qty` видны только через CDC
или сравнение снапшотов.

| Колонка | Тип | Описание |
|---|---|---|
| `id` | bigint PK | ID позиции |
| `order_id` | bigint | Заказ → `orders.id` |
| `line_no` | smallint | Номер строки в заказе, с 1 |
| `offer_id` | bigint | Оффер → `offers.id` |
| `variant_id` | bigint | Вариант (денормализовано из оффера) |
| `product_id` | bigint | Товар (денормализовано) |
| `seller_id` | integer | Продавец (денормализовано) |
| `title` | text | Название товара на момент покупки |
| `qty` | integer | Количество |
| `unit_price` | numeric(12,2) | Цена за единицу в валюте заказа, с НДС |
| `discount` | numeric(12,2) | Скидка на строку |
| `tax_rate` | numeric(5,2) | Ставка НДС, % (стандартная или сниженная страны) |
| `tax_amount` | numeric(12,2) | НДС строки |
| `line_total` | numeric(12,2) | Итог строки = `qty × unit_price − discount` |
| `commission_rate` | numeric(5,4) | Комиссия маркетплейса (доля, 0.08–0.18; `0` у 1P) |
| `status` | text | `ordered` → `shipped` → `delivered` → `returned`; или `cancelled`, `lost` |
| `returned_qty` | integer | Сколько единиц возвращено |
| `gift_wrap` ⓜ | boolean | Подарочная упаковка. Миграция `m004` (−130 дней), `NOT NULL DEFAULT false` — старые строки получают `false`, а не `NULL` |

#### `shop.order_status_history` — история статусов заказа

Append-only. При грязи встречаются дубли строк.

| Колонка | Тип | Описание |
|---|---|---|
| `id` | bigint PK | ID записи |
| `order_id` | bigint | Заказ → `orders.id` |
| `from_status` | text | Предыдущий статус; `NULL` у первой записи |
| `to_status` | text | Новый статус |
| `actor` | text | Кто изменил: `system`, `customer`, `seller`, `carrier` |
| `changed_at` | timestamptz | Момент изменения |

#### `shop.payments` — платежи

По заказу может быть несколько попыток (неудачные + успешная). При грязи бывают
**late-arriving** платежи: строка появляется в БД позже своего `created_at`.

| Колонка | Тип | Описание |
|---|---|---|
| `id` | bigint PK | ID платежа |
| `order_id` | bigint | Заказ → `orders.id` |
| `method` | text | Способ: `card`, `paypal`, `klarna`, `apple_pay`, `google_pay`, `sepa_debit`, `bank_transfer`, `cod` (наложенный платёж) и локальные: `ideal`, `bancontact`, `giropay`, `eps`, `blik`, `przelewy24`, `bizum`, `mbway`, `multibanco`, `satispay`, `swish`, `mobilepay`, `trustly`, `mia` |
| `provider` | text | PSP: `adyen`, `stripe`, `paypal`, `klarna`, `bank`; `NULL` у наложенного платежа |
| `status` | text | `pending` → `authorized` → `captured` (при отгрузке) → `partially_refunded` / `refunded`; или `failed`, `voided` (отмена авторизации), `chargeback` |
| `amount` | numeric(12,2) | Сумма в валюте заказа |
| `currency` | char(3) | Валюта |
| `refunded_amount` | numeric(12,2) | Сколько уже возвращено |
| `failure_reason` | text | Для `failed`: `card_declined`, `insufficient_funds`, `do_not_honor`, `expired_card`, `3ds_authentication_failed`, `risk_rejected`, `credit_check_failed`, `cancelled_by_user`, `timeout`, `expired`, `provider_error`, `provider_unavailable`, `cod_refused` |
| `created_at` | timestamptz | Попытка оплаты |
| `captured_at` | timestamptz | Списание денег |
| `updated_at` | timestamptz | Последнее изменение |

#### `shop.refunds` — возвраты денег

| Колонка | Тип | Описание |
|---|---|---|
| `id` | bigint PK | ID возврата денег |
| `payment_id` | bigint | Платёж → `payments.id` |
| `order_id` | bigint | Заказ → `orders.id` |
| `return_id` | bigint | Возврат товара → `returns.id`; `NULL` если деньги вернули без возврата товара |
| `amount` | numeric(12,2) | Сумма в валюте заказа |
| `reason` | text | `return`, `order_cancelled`, `seller_cancelled`, `parcel_lost` |
| `created_at` | timestamptz | Момент возврата |

#### `shop.shipments` — отправления

Заказ разбивается на отправления по продавцу / складу.

| Колонка | Тип | Описание |
|---|---|---|
| `id` | bigint PK | ID отправления |
| `order_id` | bigint | Заказ → `orders.id` |
| `seller_id` | integer | Кто отгружает → `sellers.id` |
| `warehouse_id` | smallint | Склад Lumo; `NULL` если отгружает сторонний продавец |
| `carrier` | text | Перевозчик (DHL, DPD, GLS, PostNL, InPost, Colissimo, Poșta Moldovei... ~30 штук, зависит от страны) |
| `tracking_number` | text | Трек-номер; появляется при отгрузке |
| `status` | text | `pending` → `packed` → `shipped` → `in_transit` → `delivered`; или `lost`, `returned_to_sender` (отказ / неудачная доставка), `cancelled` |
| `shipping_method` | text | `standard`, `express`, `locker` |
| `promised_at` | timestamptz | Обещанная дата доставки (**плановая**) |
| `shipped_at` | timestamptz | Передача перевозчику |
| `delivered_at` | timestamptz | Вручение |
| `created_at` | timestamptz | Создание |
| `updated_at` | timestamptz | Последнее изменение |
| `co2_grams` ⓜ | integer | Углеродный след доставки, г. Миграция `m001` (−580 дней) |

#### `shop.returns` — возвраты товара

Одна строка на позицию заказа (в пределах 14-дневного срока ЕС).

| Колонка | Тип | Описание |
|---|---|---|
| `id` | bigint PK | ID возврата |
| `order_id` | bigint | Заказ → `orders.id` |
| `order_item_id` | bigint | Позиция → `order_items.id` |
| `qty` | integer | Количество возвращаемых единиц |
| `reason` | text | `changed_mind`, `wrong_size`, `damaged`, `defective`, `not_as_described`, `quality_issue`, `late_delivery`, `better_price` |
| `status` | text | `requested` → `in_transit` → `received` → `refunded`; или `rejected` |
| `requested_at` | timestamptz | Заявка на возврат |
| `received_at` | **timestamp** | Приёмка на складе. **Без часового пояса, местное время склада** (legacy WMS): в UTC выглядит на 1–2 часа «впереди» |
| `refund_amount` | numeric(12,2) | Сумма к возврату в валюте заказа; `NULL` до решения |
| `updated_at` | timestamptz | Последнее изменение |

#### `shop.reviews` — отзывы

| Колонка | Тип | Описание |
|---|---|---|
| `id` | bigint PK | ID отзыва |
| `product_id` | bigint | Товар → `products.id` |
| `customer_id` | bigint | Автор (без FK: отзывы остаются после GDPR-удаления) |
| `order_item_id` | bigint | Покупка, по которой оставлен отзыв |
| `rating` | smallint | Оценка 1–5 (зависит от качества бренда и опыта доставки) |
| `title` | text | Заголовок |
| `body` | text | Текст |
| `language` | text | Язык отзыва (ISO 639-1) |
| `is_verified` | boolean | Подтверждённая покупка |
| `helpful_votes` | integer | Голоса «полезно» |
| `status` | text | `published`, `rejected` (модерация) |
| `created_at` | timestamptz | Публикация |
| `updated_at` | timestamptz | Последнее изменение |
| `media_count` ⓜ | smallint | Число приложенных фото/видео. Миграция `m006` (+45 дней после первого запуска) |

## Схема `sim` (служебная)

Внутреннее состояние симулятора. Меняется в той же транзакции, что и данные `shop`,
поэтому рестарт безопасен. Для аналитики не предназначено: поля — закодированные
параметры модели поведения.

#### `sim.meta` — состояние движка (одна строка, `id = 1`)

| Колонка | Тип | Описание |
|---|---|---|
| `id` | smallint PK | Всегда 1 |
| `phase` | text | `backfill` или `live` |
| `sim_time` | timestamptz | Текущее время симуляции |
| `horizon` | timestamptz | Момент первого запуска: граница истории и live; от него считаются смещения миграций |
| `start_time` | timestamptz | Начало истории (`horizon − history_days`) |
| `seed` | bigint | Seed генератора случайных чисел |
| `state` | jsonb | Сериализованное состояние движка (счётчики ID, контроллер трафика, календарь и т.п.) |
| `finalized` | boolean | Backfill завершён, индексы и FK созданы |
| `updated_at` | timestamptz | Время последнего чекпойнта (стеночное) |

#### `sim.migrations` — применённые миграции схемы `shop`

| Колонка | Тип | Описание |
|---|---|---|
| `id` | text PK | ID миграции (`m001_shipments_co2`...) |
| `applied_at` | timestamptz | Когда реально применена (стеночное время) |
| `sim_time` | timestamptz | Симулированный момент применения |

#### `sim.customer_traits` — скрытые параметры клиентов

Всё, что нужно модели поведения и не хранится в `shop.customers`. Перечисления
закодированы числами, времена — Unix-секундами (`0` = нет).

| Колонка | Тип | Описание |
|---|---|---|
| `customer_id` | bigint PK | Клиент |
| `country` | smallint | Индекс рынка |
| `address_id` | bigint | Основной адрес |
| `segment` | smallint | Поведенческий сегмент (bargain hunters, loyal premium, families...) |
| `gender` | smallint | 0 — не указан, 1 — female, 2 — male |
| `age` | smallint | Возраст |
| `device` | smallint | Предпочитаемое устройство |
| `channel` | smallint | Канал привлечения |
| `pay_pref` | smallint | Предпочитаемый способ оплаты |
| `tier` | smallint | Уровень лояльности |
| `status` | smallint | 0 — active, далее deleted / blocked |
| `flags` | smallint | Битовые флаги (opt-in, тестовый аккаунт, дети, питомцы...) |
| `active` | boolean | Ещё не ушёл (не churned) |
| `rate` | real | Интенсивность визитов |
| `price_sens` | real | Чувствительность к цене |
| `return_prop` | real | Склонность к возвратам |
| `coupon_aff` | real | Склонность к купонам |
| `satisfaction` | real | Накопленная удовлетворённость (плохой опыт ускоряет уход) |
| `spent` | real | Потрачено за период расчёта лояльности, EUR |
| `orders` | integer | Число заказов |
| `created_at` | bigint | Регистрация (Unix) |
| `updated_at` | bigint | Последнее изменение (Unix) |
| `churn_at` | bigint | Запланированный момент ухода (Unix) |
| `last_order_at` | bigint | Последний заказ (Unix) |
| `spent_at` | bigint | Начало окна подсчёта `spent` (Unix) |
| `phone_verified_at` | bigint | Подтверждение телефона (Unix) |
| `affinity` | bytea | Вектор предпочтений по департаментам |
| `repl` | jsonb | График повторных покупок расходников |

#### `sim.product_traits` — скрытые параметры товаров

| Колонка | Тип | Описание |
|---|---|---|
| `product_id` | bigint PK | Товар |
| `traits` | jsonb | Популярность, стадия жизненного цикла, поколение, качество, вирусность и т.п. |

#### `sim.offer_traits` — скрытые параметры офферов

| Колонка | Тип | Описание |
|---|---|---|
| `offer_id` | bigint PK | Оффер |
| `base_price` | numeric(12,2) | Базовая цена без акций, EUR |
| `cost` | numeric(12,2) | Себестоимость, EUR |
| `demand_ema` | real | Сглаженный спрос (для репрайсинга и закупок) |
| `promo_id` | integer | Кампания, по которой сейчас снижена цена; 0 — нет |

#### `sim.seller_traits` — скрытые параметры продавцов

| Колонка | Тип | Описание |
|---|---|---|
| `seller_id` | integer PK | Продавец |
| `traits` | jsonb | Надёжность, скорость отгрузки, склонность к отменам и т.п. |
| `shipped` | integer | Отгружено отправлений (для рейтинга) |
| `cancelled` | integer | Отменено продавцом (для рейтинга) |

#### `sim.pending` — агрегаты «в полёте»

Незавершённые заказы, корзины и закупки с их следующим шагом, чтобы live-режим
пережил рестарт.

| Колонка | Тип | Описание |
|---|---|---|
| `kind` | text PK | `order`, `cart`, `po` |
| `id` | bigint PK | ID агрегата |
| `due_at` | timestamptz | Когда наступит следующий шаг |
| `data` | jsonb | Сериализованный агрегат |
