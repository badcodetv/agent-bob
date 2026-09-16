# Connecting your Google account to Agent Bob

This guide is for the person who connects their organisation's Google account to an Agent Bob
project. You do not need to be technical. It takes about two minutes, and you only do it once.

## What this gives Bob

Once you connect, the workers in your project can use your organisation's Google account:

- **Gmail.** Bob can **read your email**: it can search your inbox and open the messages in it.
  It can also **write drafts**. Bob **will not send email**. Drafts wait in your Drafts folder
  until you open them and press Send yourself.
- **Google Drive, Docs and Sheets.** Bob can find, open, create and change files in your Drive,
  including Docs and Sheets.

Google's own screen lists these permissions when you connect. Bob asks for all of them at once.
You cannot pick some and leave others out: if you untick any box, nothing gets connected.

Only connect an account whose email and files you are happy for Bob's workers to read.

## Before you start

- You need a login for Agent Bob that is allowed to connect Google for your project. If you are
  not sure, try the steps. If you are not allowed, the button tells you (see
  [If the Connect Google button is grey](#if-the-connect-google-button-is-grey)).
- Know which Google account you are connecting: your organisation's Google account, not your
  personal one. If you are signed in to more than one Google account in this browser, Google will
  ask you to choose.
- Use one browser from start to finish. Do not copy the Google page into another browser or
  another device.

## Steps

1. Go to Agent Bob and sign in as usual.
2. If Agent Bob shows **Choose a project**, click your project.
3. In the menu on the left, click **Settings**.
4. Under **You may want to change these**, find the box called **Connections**. It shows a line
   that says **Google — Not connected**, with a **Connect Google** button next to it.
   Under that line is a list of the things that use Google, such as `gmail`, `drive`, `docs` and
   `sheets`. Each has a grey dot for now.
5. Click **Connect Google**. The page changes to Google.
6. Google asks you to choose an account or sign in. Choose your organisation's Google account.
7. Google may show a warning: **"Google hasn't verified this app"**. **This is expected.** It
   appears because Agent Bob is BadCode's own app and has not been through Google's review for
   public apps. It does not mean something is wrong. Click **Advanced**, then click
   **Go to Agent Bob (unsafe)**. (The name in that link may be written slightly differently.)
8. Google lists what Bob is asking to do with your account. Some items may have a tick box.
   **Leave every box ticked**, then click **Continue**.
9. Google sends you back to **Settings** in Agent Bob. A green message at the top of
   **Connections** says: **"Google is connected. Workers can use it from their next run."**
   The line now says **Google — Connected as** followed by your organisation's Google address,
   and the dots next to `gmail`, `drive`, `docs` and `sheets` turn green.

That's it. You can close the green message with its small cross.

Workers pick up Google from their **next** run. A chat or a job that was already running before
you connected will not see Google. A new chat, or the worker's next job, will.

Check that the address after **Connected as** is the account you meant to connect. If it is not,
disconnect (below) and connect again, choosing the right account in step 6.

## If it says something went wrong

When something goes wrong, Google still sends you back to **Settings**, and a red message at the
top of **Connections** says what happened. In every case nothing was connected, so it is safe to
click **Connect Google** again. The messages you might see:

| The message starts with | What to do |
| --- | --- |
| "Google sign-in was cancelled" | You pressed Cancel on Google's page. Click **Connect Google** again if you meant to connect. |
| "Connecting Google took too long" | You have about ten minutes from clicking the button to finishing on Google's page. Click **Connect Google** and go straight through. |
| "Google sent you back to a different browser" | Start again and finish in the same browser you started in. |
| "Some permissions were unticked on the Google screen" | Click **Connect Google** again and leave every box ticked on Google's page. |
| "Your login is not allowed to connect Google for this project" | Ask Kai (see below). |
| "Google did not give Agent Bob lasting access" | Click **Connect Google** again. |
| "Google did not accept the sign-in" | Click **Connect Google** again. If it keeps happening, ask Kai. |
| "Google approved, but Agent Bob could not save the connection" | Click **Connect Google** again. If it keeps happening, ask Kai. |
| "Connecting Google did not work" | Click **Connect Google** again. If it keeps happening, ask Kai. |

If the same message comes back twice, stop and ask Kai. Tell him the exact words of the message.

## If the Connect Google button is grey

A grey **Connect Google** button means you cannot connect Google from here yet. There is usually a
short line of grey text under it saying why.

**Ask Kai.** Send him the words under the button. The usual reasons are:

- your login is not yet allowed to connect Google for this project (Kai can allow it), or
- Connect Google has not been switched on for this Agent Bob yet (Kai switches it on).

You do not need to do anything else. Once Kai has sorted it, sign out of Agent Bob, sign in again,
and the button will work.

If Google is connected but a line in **Connections** has a grey dot and a message under it (for
example, saying the connection must be made again), click **Disconnect**, then
**Connect Google**, and follow the steps again. If that does not fix it, ask Kai.

## How to disconnect

1. Go to **Settings** and find **Connections**.
2. Next to **Google — Connected as …**, click **Disconnect**.
3. Agent Bob asks: **"Disconnect Google? Workers lose Gmail, Drive, Docs and Sheets until someone
   connects again."** Click **Disconnect**. (Click **Cancel** if you changed your mind.)
4. The message **"Google disconnected."** appears, and the line goes back to
   **Google — Not connected**.

Disconnecting also tells Google to remove Agent Bob's access to your account. Bob's workers can no
longer read your email or your files. Anything Bob already wrote, such as drafts or documents,
stays where it is in your Google account.

If instead you see **"Google disconnected here, but Google did not confirm it removed Agent Bob's
access."**, Bob has stopped using your account, but to be sure, remove it on Google's side too:

1. Go to **myaccount.google.com**, signed in as your organisation's Google account.
2. Click **Security**.
3. Find **Your connections to third-party apps & services** and open it.
4. Click **Agent Bob**, then choose to delete all its connections with your account.

You can also do this on Google's side at any time, without going through Agent Bob first. If you
do, click **Disconnect** in Settings as well, so that Settings does not go on saying Google is
connected.

To connect again later, click **Connect Google** and follow the steps from the top.
