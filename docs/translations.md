# Translating vgkeep

vgkeep's interface is English only today. Translating it does not
require knowing how to program. It requires being comfortable copying
a file, editing text, and opening a pull request - including the case
where you have only ever used GitHub through its website, which is
covered below.

## What translating involves

There are two kinds of content, and they are not the same kind of
work. Most of the app - buttons, labels, messages - comes from one
file per language, called a catalog, and translating it is a text
edit: copy a file, change some lines, open a pull request. This is
where to start. The other kind is a handful of long-form pages
(About, Terms, Privacy, Help). Translating one of these means writing
a new page file in the site's code and adding a one-line registration
to turn it on - a code change, not a text edit. If you do not write
code, the catalog by itself is already a complete and useful
contribution. For a prose page, you can still translate the text and
hand it off - paste it into a pull request description or a new
issue - for a maintainer or another contributor to turn into the page
file. Page translations are optional and can be added later, one page
at a time; they are not required to start a language.

## Reading a catalog entry

Catalogs live under `frontend/src/locales/`, one file per language.
The English file is `en.po`. A `.po` file is plain text: a list of
entries, each with the English text (`msgid`), the translation
(`msgstr`), and one or more comment lines (`#:`) pointing at the file
in the app where that text appears. Here is one real entry from
`en.po`:

    #: src/pages/NotFound.tsx
    msgid "Page not found"
    msgstr "Page not found"

In `en.po`, `msgid` and `msgstr` are always the same text, because
`en.po` is the English source. Translating means copying this file to
your language's file and changing every `msgstr` to your language,
while leaving every `msgid` untouched. The `msgid` is not just display
text - it is the key the app uses to look up the entry, so changing it
disconnects the entry instead of translating it.

`en.po` currently holds about 520 entries. That is too many to expect
in one sitting. A partial translation is still useful: any entry you
have not reached yet is shown in English until someone translates it.

## Editors

Poedit (poedit.net) is a free editor built for `.po` files. It lists
entries in a table, tracks which ones are still untranslated, and
understands the plural and "fuzzy" markers described below. It runs
on Windows, Mac, and Linux and needs no git or command-line knowledge
- open the file, translate, save.

A web-based gettext/PO editor works just as well if you prefer working
in a browser. And because a `.po` file is plain text, GitHub's own web
editor (the pencil icon on a file's page) is enough for smaller
changes, with no software to install at all.

## Placeholders

Some English text contains a placeholder in braces, such as `{name}`
or `{0}`, that gets replaced with a real value when the app runs - a
game title, a count, a date. Keep the placeholder exactly as written,
braces included, though you can move it to wherever it reads naturally
in your language.

Some entries also contain a numbered tag pair, such as `<0>` and
`</0>`, marking part of the text as a link or other formatting. Keep
both the opening and closing tag in your translation, wrapped around
whichever words should carry that formatting in your language. Here is
a real entry with both a placeholder and a tag:

    #. placeholder {0}: i18n._(d.dataType)
    #. placeholder {1}: d.label
    #: src/components/Footer.tsx
    msgid "{0} provided by <0>{1}</0>"
    msgstr "{0} provided by <0>{1}</0>"

`{0}` is a kind of data, such as "Game data". `{1}` is the name of the
service it came from, and `<0>...</0>` turns that name into a link. A
French translation, for example, might read
`msgstr "{0} fourni par <0>{1}</0>"` - the words move, but the
placeholder and the tag stay exactly as they were.

## Counts and plurals

Some entries change wording based on a number - English says "1 entry"
but "3 entries". These use a bracketed format instead of plain text:

    #. placeholder {0}: card.entry_count
    #: src/components/social/ShelfCard.tsx
    msgid "{0, plural, one {# entry} other {# entries}}"
    msgstr "{0, plural, one {# entry} other {# entries}}"

`one` is the wording for the count English treats as singular;
`other` covers every other count; `#` stands for the number itself.
Your language may draw the line differently, or need more than two
forms - Polish and Russian, for example, use three or four. Use
however many forms your language needs. Poedit shows you the correct
set of forms once you tell it which language you are translating.

## Fuzzy entries

If the English wording of an entry changes after it has already been
translated, the entry is marked "fuzzy" - a `#, fuzzy` comment line
appears just above it. This is a flag, not an error: the English
changed, so the existing translation needs a second look. Read the new
`msgid`, update `msgstr` to match if needed, and remove the `#, fuzzy`
line once you have checked it. Poedit lists fuzzy entries separately
and has a button to clear the flag.

## Starting a new language

1. Open `frontend/src/locales/en.po` in the repository.
2. Copy its contents into a new file in the same folder, named for
   your language's code - for example `fr.po` for French, `de.po` for
   German, or `pt.po` for Portuguese. Regional variants such as
   `pt-BR` are not supported yet; use the language-level code even if
   your translation targets a specific region.
3. Go through the new file and translate each `msgstr`. Leave every
   `msgid` as it is.
4. Open a pull request with the new file. If you have not done this
   before: browse to the `frontend/src/locales/` folder on GitHub, use
   "Add file", paste in your translated content, and GitHub walks you
   through proposing it as a pull request from there. No local git
   setup is needed.
5. A maintainer reviews the translation and, in the same pull request,
   adds your language to `SUPPORTED_LOCALES`, `LOCALE_NAMES`, and
   `CATALOG_LOADERS` (its `.po` file's loader) in
   `frontend/src/lib/locale.ts`. That is what turns the language on:
   the language switcher stays hidden while only English exists, and
   appears once a second language is registered.

Translating the About, Terms, Privacy, and Help pages is separate and
not required to start a language. Unlike the catalog, this is a code
contribution: someone has to write a new page file (for example
`about/About.<code>.tsx`) in valid TSX and add a one-line registration
next to the English version. Those pages stay in English for your
language until someone contributes a translated version of that
specific page; each page can be its own pull request, whenever someone
wants to take it on. If you do not write code, you can still translate
the page text and post it in the pull request description or a new
issue - a maintainer or another contributor can turn it into the page
file for you. A page shown in translation carries a short note that
the English version is the controlling text - the note is added
automatically and is not something you write yourself.

## How contributions are checked

Every pull request runs the project's automated checks: one confirms
the English catalog still matches the app's source code (this only
moves if English text itself changes, which a translation contribution
normally does not touch), and the full test suite runs. Neither check
reads translation quality - that part is on the maintainer who reviews
the pull request.
