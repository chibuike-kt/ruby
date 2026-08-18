Ruby — WhatsApp-Native Financial Assistant
Backend & AI Financial Record Engine

1. Overview
Build Ruby, a production-minded, WhatsApp-native financial assistant designed for informal traders and small businesses that regularly conduct credit-based transactions.
Ruby allows a trader to manage informal credit entirely through natural conversation on WhatsApp.
A trader should be able to send a text or voice message such as:
"Chinedu took two cartons of noodles for ₦75,000. He'll pay me Friday."
Ruby must interpret the message, identify the intended customer and financial action, validate the extracted information, persist the transaction, and respond with a clear confirmation.
Ruby must also support:
Recording credit/debt transactions
Recording full and partial repayments
Tracking outstanding balances
Querying customers and balances
Scheduling trader reminders
Sending customer payment reminders where enabled
Handling multiple customers with identical names
Handling ambiguous natural-language instructions
Voice-note input
Persistent financial records independent of WhatsApp chat history
Financial transaction history and auditability
Customer confirmation workflows as a future-ready capability
The core architectural principle is:
WhatsApp is the interface. Ruby's backend is the source of truth.
A user's financial records MUST NOT depend on the existence of their WhatsApp chat history.
If a user loses their phone, deletes their WhatsApp conversation, reinstalls WhatsApp, or changes devices, their Ruby financial records must remain intact.

2. Core Product Philosophy
Ruby is not intended to be a traditional accounting application.
The user should not have to:
Open application
→ Find customer
→ Create transaction
→ Select transaction type
→ Enter amount
→ Select date
→ Save
Instead:
Trader speaks naturally
        ↓
Ruby understands
        ↓
Ruby validates
        ↓
Ruby records
        ↓
Ruby responds
Ruby must therefore optimize for:
Minimal user effort
Natural language
Voice interaction
Financial correctness
Recoverability
Auditability
Ambiguity resolution
The system must never sacrifice financial correctness for conversational convenience.

3. Core Functional Specification
A. WhatsApp Integration
Ruby must operate primarily through the Meta WhatsApp Cloud API.
Incoming message flow
Trader
  ↓
WhatsApp
  ↓
Meta WhatsApp Cloud API
  ↓
Ruby Webhook
  ↓
Message Processing
  ↓
Intent Extraction
  ↓
Validation
  ↓
Financial Service
  ↓
Database
  ↓
WhatsApp Response
The webhook must:
Verify webhook requests where applicable.
Validate incoming message structure.
Identify the Ruby user associated with the WhatsApp number.
Store provider message IDs.
Prevent duplicate processing of the same WhatsApp event.
Immediately acknowledge the webhook where possible.
Dispatch expensive processing to background jobs when appropriate.
The webhook must not perform long-running AI or financial operations synchronously if doing so risks provider timeout or duplicate delivery.

4. User Account Model
Every Ruby trader must have an internal immutable user_id.
The WhatsApp phone number must not be treated as the permanent financial identity.
Example:
Ruby User
---------
id: 18291
name: Musa
business_name: Musa Trading
whatsapp_number: +2348012345678
Financial records must reference:
user_id = 18291
rather than relying exclusively on:
whatsapp_number
This allows future account recovery and phone-number changes.

5. Persistent Financial Records
This is a mandatory requirement.
Ruby must persist all financial records in its own backend.
WhatsApp chat history must never be considered the financial source of truth.
For example:
WhatsApp Chat
     ↓
Deleted
     ↓
Ruby financial records
     ↓
STILL AVAILABLE
A user losing their WhatsApp chat must not result in loss of:
Customers
Debts
Payments
Outstanding balances
Due dates
Transaction history
Reminder configuration
Ledger history
Required architecture
WhatsApp
    ↓
Ruby API
    ↓
PostgreSQL
    ↓
Automated Backups
The database is authoritative.

6. Customer Management
Every customer must have an internal unique identifier.
Example:
Customer ID: CUS-1042
Name: Chinedu Okafor
Phone: +2348012345678
Alias: Chinedu Mechanic
Trader ID: 18291
Customer names MUST NOT be unique identifiers.

7. Duplicate Customer Names
Ruby must explicitly handle multiple customers with the same name.
Example:
Customer A
Name: Chinedu
Phone: 0803 XXX 1234
Outstanding: ₦50,000

Customer B
Name: Chinedu
Phone: 0907 XXX 5678
Outstanding: ₦120,000
If the trader says:
"Chinedu paid me ₦50,000."
Ruby must not blindly select one.
Ruby should respond:
I found 2 customers named Chinedu:
1.Chinedu — 0803 XXX 1234 — ₦50,000 outstanding
2.Chinedu — 0907 XXX 5678 — ₦120,000 outstanding
3.Which one do you mean?
4.The user may respond:
Ruby then resolves the internal customer_id.

8. Customer Identity Resolution
Ruby should use the following identity signals, in descending order of reliability:
1.Internal customer_id
2.Customer WhatsApp/phone number
3.Explicit user selection from an ambiguity prompt
4.Recent conversation context
5.Customer alias
6.Exact name match
Name alone must never be sufficient when multiple records exist.
Ruby should maintain conversational context where practical.
Example:
Trader:
"Chinedu took ₦75k and will pay Friday."
Ruby creates:
CUS-1042
Trader:
"He paid me 30k."
Ruby may resolve "he" to the customer from the immediately preceding transaction.
However, if context is ambiguous, Ruby must ask.

9. Natural Language Intent Engine
Ruby must translate user messages into structured intents.
Initial supported intents:
CREATE_DEBT
RECORD_PAYMENT
LIST_CUSTOMERS
GET_CUSTOMER_BALANCE
LIST_OUTSTANDING_DEBTS
GET_TOTAL_OUTSTANDING
GET_PAYMENT_SUMMARY
CREATE_REMINDER
CANCEL_REMINDER
CONFIRM_ACTION
DISAMBIGUATE_CUSTOMER
EXPORT_RECORDS
HELP
Example:
"Chinedu took 3 bags of rice for 90k and will pay next Friday."
should produce structured data similar to:
{
  "intent": "CREATE_DEBT",
  "customer_name": "Chinedu",
  "amount": 90000,
  "currency": "NGN",
  "description": "3 bags of rice",
  "due_date": "2026-08-28"
}
The AI output must never directly mutate the database.

10. AI Financial Safety Rule
AI interprets. Backend validates. Ledger records.
The AI is responsible for understanding language.
The backend is responsible for:
Validating amounts
Resolving identities
Checking balances
Checking transaction state
Checking dates
Enforcing business rules
Executing database transactions
The AI must never be trusted as the authoritative financial system.

11. Ambiguous Instructions
Ruby must not guess when ambiguity could cause a financial error.
Example:
"Chinedu paid me."
If two Chinedus exist:
Ruby must ask which one.
Likewise:
"He paid me."
If the previous conversation contains several possible customers, Ruby must ask for clarification.
Example:
Which customer are you referring to?

12. Debt Creation
Ruby must support credit transactions.
Example:
"Ngozi took five bags of rice for ₦120,000. She'll pay next week."
The system should create:
Debt
----
Customer: Ngozi
Principal: ₦120,000
Paid: ₦0
Outstanding: ₦120,000
Due Date: 25 Aug 2026
Status: OUTSTANDING
The original transaction must be persisted.

13. Partial Payments
Ruby MUST support partial payments.
Example:
Original:
Debt = ₦120,000
Customer pays:
₦50,000
Ruby must record:
Debt
₦120,000

Payment
-₦50,000

Outstanding
₦70,000
If another payment of:
₦20,000
is received:
Outstanding = ₦50,000
Ruby must never overwrite the original debt amount.

14. Overpayment Protection
If:
Outstanding = ₦75,000
and the trader says:
"Chinedu paid ₦100,000."
Ruby must not blindly create a ₦100,000 payment.
It should ask:
Chinedu currently owes ₦75,000. You entered ₦100,000. Did you mean to record ₦75,000 as the final payment?
Any excess amount should require an explicit business rule or separate transaction.

15. Duplicate Payment Protection
Ruby must prevent the same payment event from being recorded twice.
Every externally identifiable message/event should have an idempotency mechanism.
For example:
WhatsApp message ID
Provider payment reference
Internal idempotency key
must be uniquely tracked.
If the same event arrives twice:
Request 1 → Payment recorded
Request 2 → Existing event detected
The second request must not modify the balance again.

16. Financial Ledger
Ruby should maintain an auditable financial event history.
Example:
18 Aug
DEBT_CREATED       +₦120,000

20 Aug
PAYMENT_RECORDED   -₦50,000

23 Aug
PAYMENT_RECORDED   -₦30,000

25 Aug
PAYMENT_RECORDED   -₦40,000

25 Aug
DEBT_SETTLED
The ledger/event history must allow Ruby to determine exactly how the current outstanding amount was reached.
Financial events should not simply be deleted or overwritten.

17. Balance Calculation
The system must distinguish between:
Original debt
Total payments
Outstanding balance
Conceptually:
Outstanding =
Original Debt
-
Sum(Valid Payments)
All calculations must use integer minor units.
For NGN:
₦1 = 100 kobo
Therefore:
₦75,000
=
7,500,000 kobo
No floating-point arithmetic should be used for financial calculations.

18. Reminder System
Ruby must support two separate reminder recipients.
Trader reminder
Example:
🔔 Ruby Reminder
Chinedu's ₦75,000 payment is due today.
Customer reminder
Where enabled and where the customer has an appropriate contact:
Hi Chinedu 👋
This is a friendly reminder that you have an outstanding balance of ₦75,000 with Musa Trading.
Due date: August 21.
Customer reminders must require appropriate user configuration/consent and must comply with applicable WhatsApp business messaging requirements.

19. Reminder Architecture
A reminder should be stored as its own entity.
Example:
Reminder
--------
id
debt_id
recipient_type
recipient_id
scheduled_at
status
template
provider_message_id
sent_at
Possible recipient types:
TRADER
CUSTOMER
Reminder jobs should be processed asynchronously.

20. Reminder Failure Handling
If a customer reminder fails:
Reminder
status = FAILED
The system should store:
Failure reason
Attempt count
Timestamp
The system must not silently assume delivery.
Possible states:
SCHEDULED
PROCESSING
SENT
FAILED
CANCELLED

21. Due-Date Edge Cases
Ruby must handle:
No due date
User:
"Chinedu owes me 50k."
Ruby records the debt but should not invent a due date.
Relative date
"He'll pay Friday."
Ruby must resolve the date using the configured timezone.
Past date
"He was supposed to pay yesterday."
Ruby should record the due date as past and classify it as overdue.
Ambiguous date
"He'll pay next week sometime."
Ruby may store a less precise expectation or ask for a specific date depending on the implementation.

22. WhatsApp Voice Notes
Ruby must support voice-note input.
Pipeline:
Voice Note
    ↓
WhatsApp Media API
    ↓
Audio Retrieval
    ↓
Speech-to-Text
    ↓
Intent Extraction
    ↓
Entity Extraction
    ↓
Validation
    ↓
Financial Operation
Ruby should be designed to eventually support:
English
Nigerian Pidgin
Yoruba
Igbo
Hausa
For the hackathon, reliability takes priority over supporting every language.

23. Voice Recognition Safety
Voice transcription can incorrectly interpret:
Names
Numbers
Currency
Dates
Therefore Ruby should use confirmation for uncertain financial instructions.
Example:
I understood:
Chinedu
₦75,000
Due Friday
Should I record this?
For high-confidence low-risk queries, Ruby may avoid unnecessary confirmation to preserve conversational speed.

24.Customer Reminders and Customer Accounts
A customer should not necessarily need a Ruby account to receive a reminder.
The trader may provide:
Customer:
Chinedu

WhatsApp:
+2348012345678
Ruby can associate the number with the customer's record.
However, the system must avoid exposing sensitive information to an unverified recipient.
Customer communication should reveal only information necessary for the reminder.

25. Customer Confirmation — Future/Optional MVP Feature
Ruby should be architected so that a customer can eventually receive:
Musa has recorded a credit transaction of ₦75,000 for goods received.
Customer:
1. Confirm
2. Dispute
If confirmed:
Trader recorded ✓
Customer confirmed ✓
This creates a stronger foundation for future verified financial reputation.
This feature is not mandatory for the three-day MVP, but the data model should not prevent it.

26. Account Recovery
Ruby must support the principle that:
Losing WhatsApp history must not mean losing financial history.
The internal model should be:
Ruby User ID
     │
     ├── WhatsApp number
     ├── Customers
     ├── Debts
     ├── Payments
     ├── Ledger
     └── Reminders
not:
WhatsApp chat
     └── everything
Future recovery mechanisms may include:
Verified phone-number change
Email
PIN
Identity verification
Trusted recovery mechanism
Authenticated dashboard

27. Phone Number Change
If a trader changes their WhatsApp number, the financial records must remain attached to the same internal account.
Example:
User #18291

Old number
+234801...

        ↓

New number
+234907...
The account remains:
User #18291
The number is updated after appropriate verification.

28. Data Export
Ruby should eventually support:
"Export my records."
Ruby can generate:
PDF statement
CSV transaction export
The statement could include:
Business: Musa Trading

Total credit issued: ₦6,840,000
Total collected: ₦5,920,000
Outstanding: ₦920,000

Outstanding Customers
---------------------
Chinedu    ₦75,000
Ngozi      ₦120,000
Ada        ₦45,000
For the hackathon, a dashboard export can be considered optional if time is limited.

29. Dashboard
The dashboard is secondary to WhatsApp.
It should provide:
Overview
Total Outstanding
Total Collected
Total Credit Issued
Overdue Amount
Active Customers
Customers
Customer
Phone
Outstanding
Last Transaction
Status
Transactions
Date
Customer
Type
Amount
Balance
Status
Reminders
Customer
Amount
Scheduled Time
Recipient
Status
The dashboard exists primarily for:
Visibility
Recovery
Administration
Demo
Data ownership

30. Data Model
Minimum required entities:
users
id
name
phone_number
business_name
created_at
updated_at
customers
id
user_id
name
phone_number
alias
created_at
updated_at
debts
id
user_id
customer_id
amount_minor
currency
description
due_date
status
created_at
updated_at
payments
id
debt_id
amount_minor
currency
idempotency_key
created_at
ledger_entries
id
user_id
debt_id
type
amount_minor
currency
reference
metadata
created_at
reminders
id
debt_id
recipient_type
recipient_id
scheduled_at
status
attempts
provider_message_id
created_at
messages
id
user_id
provider_message_id
direction
message_type
content_reference
processing_status
created_at

31. Idempotency
All externally initiated financial actions must be idempotent.
For example:
WhatsApp Event ID
must not be processed twice.
If:
POST /payment
Idempotency-Key: abc123
is received twice:
First → payment created
Second → existing result returned
There must never be:
First → ₦30,000 deducted
Second → another ₦30,000 deducted

32. Database Transactions
Financial mutations must occur inside database transactions.
For a payment:
BEGIN

Validate debt
Validate payment
Check remaining balance
Create payment
Create ledger event
Update derived state if applicable

COMMIT
If any operation fails:
ROLLBACK
No partially applied payment should remain.

33. Concurrency
Ruby must be safe when two payments or commands arrive simultaneously.
Example:
Outstanding = ₦50,000

Request A → pay ₦40,000
Request B → pay ₦40,000
Ruby must not end with:
Outstanding = -₦30,000
The system must use appropriate database locking/transaction isolation and atomic checks.
Expected result:
Request A → SUCCESS
Request B → REJECTED
or another deterministic conflict resolution.

34. Race Condition Test
The test suite should include a concurrency test.
Setup:
Customer owes ₦50,000
Fire 10 concurrent requests:
Payment = ₦50,000
Expected:
Exactly 1 successful payment
Exactly 9 rejected/duplicate/conflict requests
Outstanding = ₦0
There must never be:
10 payments
Negative balance
Duplicate ledger events

35. Security Requirements
The system must:
Authenticate WhatsApp users correctly.
Verify provider webhook signatures where applicable.
Protect API secrets.
Prevent cross-user data access.
Validate all AI-generated parameters.
Prevent replayed messages.
Use idempotency keys.
Sanitize logs.
Avoid exposing sensitive customer data.
Use HTTPS in production.
Protect database credentials.
Apply rate limiting to appropriate endpoints.
Record security-relevant events.

36. Privacy Boundary
Ruby handles financial information.
Therefore the system must minimize unnecessary exposure of:
Customer phone numbers
Transaction amounts
Customer names
Message content
Logs should prefer:
user_id
transaction_id
event_type
timestamp
rather than dumping entire conversations into application logs.

37. Error Handling
Ruby should never respond with an unexplained technical error.
Instead:
Internal error
Something went wrong while recording that transaction. Your existing records are safe. Please try again.
Ambiguous customer
I found two customers named Chinedu. Which one do you mean?
Invalid amount
I couldn't determine the amount. How much did Chinedu owe you?
Duplicate action
That payment has already been recorded.
Missing customer number for reminder
I don't have Chinedu's WhatsApp number yet. Send me his number if you'd like me to send reminders.

38. State Machine
Debts should have controlled states.
Example:
OUTSTANDING
     ↓
PARTIALLY_PAID
     ↓
SETTLED
An overdue debt can be represented independently or through:
OUTSTANDING + due_date < now
Invalid transitions must be rejected.
For example:
SETTLED
   ↓
PAYMENT
must not happen without an explicit correction/refund workflow.

39. Audit Trail
Important events should be auditable.
Examples:
DEBT_CREATED
PAYMENT_RECORDED
DEBT_SETTLED
REMINDER_CREATED
REMINDER_SENT
REMINDER_FAILED
CUSTOMER_CREATED
CUSTOMER_UPDATED
ACCOUNT_RECOVERED
Each event should contain enough metadata to understand:
Who initiated it
What object was affected
When it happened
What changed
Which external provider event caused it, if applicable

40. Out-of-Order Events
The system should not assume external events arrive in perfect order.
For example:
PAYMENT_CONFIRMED
could arrive before:
PAYMENT_INITIATED
The system must use state validation rather than blindly applying events.
If an event cannot safely be processed yet, it should be:
Deferred
Reconciled
Marked for review
rather than corrupting financial state.

41. Performance and Queues
The following operations should be candidates for asynchronous processing:
Voice transcription
AI processing
Reminder delivery
PDF generation
Exports
Non-critical analytics
The core financial transaction should remain deterministic and transactional.

42. API / Internal Service Boundaries
Suggested services:
WhatsAppService
MessageProcessingService
VoiceTranscriptionService
AIIntentService
CustomerResolutionService
DebtService
PaymentService
LedgerService
ReminderService
AccountRecoveryService
ExportService
The team may implement these as service classes/actions according to the chosen architecture.
The key requirement is separation of:
AI interpretation
≠
financial mutation

43. Required API Capabilities
A possible internal/public API:
Method	Endpoint	Purpose
POST	/api/webhooks/whatsapp	Receive WhatsApp events
POST	/api/debts	Create debt
POST	/api/debts/{id}/payments	Record payment
GET	/api/debts	List debts
GET	/api/customers	List customers
GET	/api/customers/{id}	Customer details
GET	/api/summary	Financial summary
POST	/api/reminders	Create reminder
GET	/api/reminders	List reminders
GET	/api/ledger	Financial event history
GET	/api/export	Export records
The WhatsApp interface may invoke these services internally rather than exposing every endpoint publicly.

44. Three-Day Hackathon Scope
Day 1 — Financial Core
Build:
Database schema
Users
Customers
Debts
Payments
Ledger
Balance calculation
Partial payments
Overpayment protection
Idempotency
Transaction safety
Duplicate-name resolution
Goal: Ruby's financial engine works without WhatsApp.

Day 2 — WhatsApp + AI
Build:
WhatsApp webhook
Incoming messages
Outgoing messages
Text intent extraction
Voice-note processing
Structured AI output
Customer resolution
Natural-language responses
Goal: A trader can conduct the financial workflow entirely through WhatsApp.

Day 3 — Reminders + Reliability + Demo
Build:
Trader reminders
Customer reminders
Reminder queue
Dashboard
Persistence demonstration
Recovery flow/basic account lookup
Concurrency tests
Idempotency tests
Error handling
Final UI/UX polish
Goal: Demonstrate that Ruby isn't merely an AI chatbot—it is a reliable financial system.

45. Mandatory Testing Requirements
The project should include tests for:
Financial calculations
Correct debt creation
Partial payment
Full payment
Multiple payments
Zero payment
Invalid payment
Overpayment
Settlement
Identity
Duplicate names
Same name/different phone
Same customer/different aliases
Ambiguous customer
Contextual customer resolution
Reliability
Duplicate WhatsApp event
Duplicate payment
Concurrent payments
Failed transaction rollback
Reminder retry
Failed message delivery
Persistence
Delete WhatsApp chat
Query backend
Verify records remain
Security
Invalid webhook
Unauthorized user
Cross-user customer access
Invalid AI output
Malformed message

46. Mandatory Demo Scenario
The final demonstration should tell one complete story.
Step 1 — Create credit
Voice:
"Chinedu took two cartons of noodles for ₦75,000. He'll pay Friday."
Ruby:
Debt recorded ✓
Chinedu — ₦75,000
Due — Friday
Step 2 — Query
"Who owes me?"
Ruby returns outstanding customers.
Step 3 — Partial payment
"Chinedu paid me ₦30,000."
Ruby:
Payment recorded ✓
Remaining: ₦45,000
Step 4 — Duplicate names
Create another Chinedu.
Then:
"Chinedu paid me 20k."
Ruby asks which Chinedu.
Step 5 — Reminder
"Remind Chinedu tomorrow."
Ruby schedules the reminder.
Step 6 — Customer reminder
Show Ruby sending the configured customer reminder.
Step 7 — Persistence
Clear/delete the WhatsApp conversation.
Show the dashboard/backend.
The records remain:
Chinedu
Original debt: ₦75,000
Paid: ₦30,000
Outstanding: ₦45,000
Final message to judges:
"The conversation can disappear. The financial record doesn't."

47. Future Financial Inclusion Layer
The hackathon should not attempt to build lending or banking products.
Instead, Ruby creates the infrastructure that could eventually enable them.
The progression is:
Informal Commerce
       ↓
Ruby Records
       ↓
Structured Financial History
       ↓
Customer Confirmation
       ↓
Verified Repayment History
       ↓
Financial Reputation
       ↓
Potential Access to Financial Services
Future possibilities include:
Working-capital access
Supplier credit
Business insurance
Savings products
Cash-flow-based financial products
Portable financial reputation
Any future credit product must be designed separately with appropriate underwriting, consent, regulatory, and risk controls.

48. Non-Goals
The three-day version MUST NOT attempt to build:
A wallet
A bank account
A lending platform
A payment gateway
A POS system
Full accounting software
Inventory management
A marketplace
A credit-scoring product
Full multilingual support
Complex KYC infrastructure
Banking integrations
Automated lending decisions
These may be future extensions.

49. Definition of Done
Ruby's MVP is considered complete when:
A trader can interact with Ruby through WhatsApp.
A trader can record a debt using natural language.
A trader can record a debt using voice.
Ruby can extract customer, amount, and due date.
Ruby can record partial payments.
Ruby can calculate outstanding balances correctly.
Ruby prevents overpayments from being silently accepted.
Ruby prevents duplicate financial events.
Ruby can answer "Who owes me?"
Ruby can answer individual customer balance questions.
Ruby handles duplicate customer names.
Ruby can schedule trader reminders.
Ruby can send configured customer reminders.
Financial records persist independently of WhatsApp chat history.
Financial records are auditable.
Concurrent financial operations cannot create invalid balances.
Cross-user data access is prevented.
The system has automated tests covering the critical financial paths.
The complete flow can be demonstrated end-to-end.

50. Final Product Definition
Ruby is a WhatsApp-native financial assistant that allows informal businesses to record, track, collect, and understand credit transactions through natural conversation.
The most important architectural distinction is:
WhatsApp is the interface. Ruby is the financial record.
The most important financial principle is:
AI interprets. Deterministic backend logic validates. The ledger records.
The most important inclusion principle is:
The user should not need to learn a new financial application to participate in structured financial record keeping.
And the long-term vision is:
Ruby turns informal trust and commerce into structured, persistent, and potentially verifiable financial history.
One-line pitch
Ruby helps informal businesses turn trust into a financial record — directly through WhatsApp.
