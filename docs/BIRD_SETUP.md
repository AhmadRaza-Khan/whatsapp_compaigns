# Bird setup runbook

Do these steps in order, before go-live. Steps 3 and 4 need the client present with their phone and Meta (Facebook) login. Write every ID you collect into `docs/HANDOVER.md`.

## 1. Organization and access

1. Sign in at https://app.bird.com with the client's account (or create one with the client's business email).
2. Create the client's own organization in the **eu1 (EU)** region. The region cannot be changed later.
3. Billing: add the **client's card**, not Cyberboost's.
4. Settings > Members: invite Cyberboost's support email as a member.

## 2. Plan

The plan sets the sending speed. Pay-as-you-go sends only 10 messages per minute, so 2,500 customers would take about 4 hours.

| Plan | Price | Speed | Time for 2,500 |
|---|---|---|---|
| Startup (minimum) | $15/month | 30/min | ~83 min |
| Growth | $100/month | 100/min | ~25 min |

The monthly deposit becomes spendable balance.
**In the platform:** Settings > Send speed must match the plan (30 or 100).

## 3. Connect the number (coexistence), with the client

Check before starting:
- The client's phone runs **WhatsApp Business 2.24.17 or later** (App Store or Play Store > update).
- Tell the client that linked devices (WhatsApp Web/desktop) disconnect during setup and must be re-linked afterwards.
- Old broadcast lists become read-only. Group chats stay in the app only.

Steps:
1. Bird > **WhatsApp > Numbers & profiles > New number**.
2. Choose **Keep using the Business app (coexistence)**.
3. The client logs in with Meta, picks or creates the Business Portfolio, and scans the QR code / confirms on the phone.
4. Have the client re-link WhatsApp Web/desktop.
5. Business name, photo and description are edited **in the WhatsApp Business app**, not in Bird.

Record: **WABA ID**, **phone number ID**, the number in E.164 (e.g. `+97455123456`).
**In the platform:** Settings > Sender number = the number in E.164 format, e.g. `+97455123456` (Bird requires E.164 here, not the number ID). Settings > WABA ID = the WABA ID.

## 4. Meta Business Verification and display name

1. In Meta Business Suite > Security Centre, start **Business Verification** (trade licence or CR, utility bill, website matching the business name).
2. Wait for the display name approval.
3. In Bird, open the number and check:
   - **Quality rating** (should be green),
   - **Messaging limit**: it must cover the full list. 2,500 customers needs at least the 10K/24h tier. A new number starts at 250/24h and Meta raises it after verification and good-quality sending,
   - **Marketing Messages API onboarding** status.

Until the limit covers the full list, send campaigns in batches (one group per day).

## 5. API key

1. Bird > Settings > **API keys** > create a **workspace** key named `campaign-platform`.
2. Scopes needed: WhatsApp messages (send + read), WhatsApp templates/management (read + write), webhooks (write), preferences (read + write).
3. It starts with `bk_eu1_`. Put it **only** in the server's `.env` as `BIRD_API_KEY`. Never in the browser, Git or chat.
4. Check: platform > Settings > **Test Bird** shows "Connected".

## 6. First templates

**Option A (recommended): from the platform.** Templates > New template. This builds the tracked Shop Now button automatically.

Marketing launch template, e.g. `collection_launch`:
- Header: image (example URL of a product photo)
- Body: `Hi {{1}}, our new {{2}} collection has arrived! {{3}}` with examples `Sara`, `Winter`, `Enjoy 20% off this week.`
- Footer: `Reply STOP to unsubscribe`
- Buttons: URL **Shop Now** → `https://<DOMAIN>/r/{{1}}` (dynamic suffix), quick reply **Stop promotions**

Optional utility template (e.g. `order_update`): body `Hi {{1}}, your order {{2}} is ready for pickup.`

**Option B: in Bird's dashboard.** The URL button must be **dynamic** with base URL exactly `https://<DOMAIN>/r/{{1}}` (copy it from platform > Settings), otherwise clicks are not tracked.

Approval takes minutes to 24h. Then platform > Templates > **Sync from Bird**. Status must be `active` with the language `approved`.

## 7. Webhooks

Create one subscription pointing to the platform. In the dashboard: Bird > Developers/Settings > **Webhooks** > New. Or by API:

```bash
curl -X POST https://eu1.platform.bird.com/v1/webhooks \
  -H "Authorization: Bearer $BIRD_API_KEY" -H "Content-Type: application/json" \
  -d '{
    "url": "https://<DOMAIN>/webhooks/bird",
    "description": "Campaign platform",
    "events": ["whatsapp.accepted","whatsapp.sent","whatsapp.delivered","whatsapp.read",
               "whatsapp.failed","whatsapp.rejected","whatsapp.received",
               "preference.revoked","preference.granted","whatsapp_suppression.created"]
  }'
```

The response contains `secret` (`whsec_…`) **once**. Put it in `.env` as `BIRD_WEBHOOK_SECRET` and restart: `docker compose up -d`. The platform rejects any webhook whose signature doesn't match. If the secret is lost, rotate it (`POST /v1/webhooks/{id}/rotate-secret`).

Record the **webhook ID** and URL.

## 8. Opt-outs

- Bird's built-in STOP keywords block later sends automatically.
- The platform also catches STOP replies itself (keywords in Settings, including Arabic and the "Stop promotions" button), marks the contact opted out, and records the opt-out in Bird's preference store.
- `preference.revoked` / `whatsapp_suppression.created` webhooks from Bird mark contacts opted out in the platform.
- Contacts > **Sync opt-outs** pulls Bird's full opt-out list (run it once after setup).

## 9. Test with Cyberboost's own numbers

1. Import a CSV with 3 to 10 Cyberboost phones into a group "Test group".
2. Create a campaign with the launch template, send a **test** to your phone, then **Send now** to the group.
3. In the campaign report, check that **delivered** and **read** appear within a minute and that tapping **Shop Now** shows up as a click.
4. From one test phone reply **STOP**. Check that the contact shows "opted out" and that a second campaign to the group has one fewer recipient.
5. Schedule a campaign 5 minutes ahead and confirm it starts on time.
6. Compare the campaign's estimated cost with the actual cost after ~5 minutes.

If delivered/read don't arrive, check `docker compose logs api | grep webhook` (a signature mismatch means the wrong secret) and Bird > Webhooks > deliveries.
