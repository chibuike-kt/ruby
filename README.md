# Ruby

## Team Members

* Kingsley Chibueze
* Adegbanke Solomon
* Tolulola Tolulope

## 🚀 Live Demo

* Message Ruby directly on WhatsApp: https://api.whatsapp.com/send?phone=2347048700500&text=Hi%20Ruby
* Marketing site (secondary, not the product itself): https://ruby-flax-two.vercel.app/
* Recorded Demo: [Link to your Loom walkthrough]

## 🎯 The Problem

**How might we help financially excluded informal traders build a verifiable credit history, using nothing more than the tools they already use every day?**

Ruby tackles the first half of this directly: most informal traders in Nigeria extend credit constantly, but that history exists only in memory, a notebook, or a WhatsApp chat that can vanish the moment a phone is lost or a chat is deleted. Ruby turns that same WhatsApp conversation the trader already uses into a permanent, structured, verifiable record, with no new app to learn. The second half, that record becoming usable credit history a bank can act on, is what the Credit Profile API and the Wema pathway below are built toward.

## ✨ Our Solution

Ruby starts with the single most common, highest friction problem an informal trader has: never dependably knowing who owes them what. That is a deliberate, narrow entry point, not the ceiling of the idea. A trader tells Ruby who took what and when they will pay, in plain language, by text or voice, in English, Nigerian Pidgin, Yoruba, Igbo, or Hausa. Ruby understands the message, records it to a real financial ledger, and confirms it back, all inside the same WhatsApp conversation the trader already uses every day.

The core idea: WhatsApp is the interface, but it is never the source of truth. Every transaction Ruby records lives in its own database, independent of the chat itself, so a deleted conversation, a lost phone, or a new number never means a lost financial record.

Most AI powered WhatsApp bots let a language model talk directly to a database. Ruby does not. The AI's only job is to understand what a trader said. A deterministic backend, with no AI involved, validates every amount, resolves every customer, checks every balance, and enforces every business rule before anything is written to the ledger. The AI proposes, the backend decides. Concretely, this means:

* **Payments cannot double up, even under real concurrency.** Ten simultaneous requests to pay off the same debt result in exactly one successful payment and nine correctly rejected duplicates, enforced with row level database locking, not application level guessing. This is verified with an automated test that fires the requests in parallel and checks the outcome, not just a manual click test.
* **The AI cannot state a number it was not given.** Every phrased reply Ruby sends is checked against the actual data it was built from before it is sent. If a reply contains a figure that cannot be traced back to a real field in the underlying record, it never reaches the trader. This closes off an entire class of AI hallucination that would otherwise be invisible in a financial product.
* **Two customers with the same name are never silently merged or guessed apart.** Ruby asks. If a trader has two people named Chinedu, Ruby resolves the ambiguity by phone number, transaction context, or a direct question, never by assumption.
* **Every trader's data is fully isolated from every other trader's**, enforced at the database and service layer, not just hidden in the interface.
* **Multilingual support handles real code switching**, not just single language detection. A message that mixes English and Pidgin in the same sentence, which is how people actually talk, is understood correctly rather than forcing a single language guess.

The bigger picture is what happens once that record exists. An informal trader's real repayment behavior is exactly the kind of data traditional banking has no visibility into, which is a large part of why so many are excluded from formal credit. Ruby is not trying to become a bank, a wallet, or a lender. It is building the verified, structured record that makes it possible for one to say yes. See the Wema section below for how this becomes real, not just aspirational.

## 🏦 Pathway to Wema

This hackathon is hosted by Wema Bank, and the brief asks for a clear path into Wema's ecosystem. We treated that as a real engineering requirement, not only a pitch slide.

Ruby exposes a consent gated, read only **Credit Profile API**. A trader must explicitly opt in, off by default, before any data is shareable at all. When opted in, a partner like Wema can query a summary of that trader's verified credit behavior: account age, total credit issued, total collected, on time payment rate, active customer count, outstanding balance. Never raw transaction detail, never a customer's private information, only an aggregate signal a lender could use to extend working capital faster and to more people than a traditional credit check ever could.

Ruby deliberately never moves money, never holds a wallet, and never makes a lending decision. It stays a bookkeeping tool that produces a trustworthy record. The underwriting, the risk assessment, and the actual decision to lend stay entirely with Wema. That boundary is what makes the pathway credible instead of an overreach: we are not claiming to have built a lending product in a hackathon, we are showing exactly the data contract a real one would need, already working.

## 🔍 Engineering Highlights

A quick reference for anyone reviewing the codebase directly.

* **Financial correctness under concurrency**, proven with an automated race condition test, not asserted in prose.
* **A structural boundary between AI and money**, enforced in the type system and re verified with tests every time the phrasing layer changes.
* **Idempotent payment recording**, so a retried WhatsApp message or a network hiccup can never charge a customer twice.
* **An append only, auditable ledger**, so the current balance is always reconstructable from history, never just a mutable number.
* **A working, consent gated Credit Profile API for Wema**, built as real, tested code, not only described in a slide.

## 🛠️ Tech Stack

* Frontend: Next.js (App Router), React, Tailwind CSS, Framer Motion
* Backend: Go, PostgreSQL, Redis
* AI and APIs: OpenAI (structured intent extraction and voice transcription), Meta WhatsApp Cloud API
* Infrastructure: Docker and Docker Compose for local orchestration, ngrok for tunneling the local backend to a public webhook URL during development

## ⚙️ How to Set Up and Run Locally

This is the backend, the actual product: the API, the AI pipeline, and the WhatsApp integration. The marketing landing page lives in a separate repository and is linked at the top of this README under Live Demo, it is not required to run or evaluate Ruby itself.

1. Clone the repository:

```
git clone [backend repo link]
cd ruby
```

2. Copy the environment template and fill in real values (WhatsApp Cloud API credentials, OpenAI API key, database and Redis connection strings):

```
cp .env.example .env
```

3. Start Postgres and Redis, then build and run the API:

```
docker compose up -d --build
```

4. Run the database migrations:

```
migrate -path ./migrations -database "postgres://ruby:ruby@localhost:5432/ruby?sslmode=disable" up
```

5. To receive real WhatsApp messages, tunnel the API with ngrok and register the resulting URL as the webhook in Meta's developer dashboard:

```
ngrok http 8080
```

6. Message the connected WhatsApp number to start using Ruby directly. This is the actual product experience, the interface is WhatsApp itself, not a browser.
