---
date: "2019-11-21T17:00:00-03:00"
title: "Usage: Automatically Linked References"
slug: "automatically-linked-references"
weight: 15
toc: true
draft: false
menu:
  sidebar:
    parent: "usage"
    name: "Automatically Linked References"
    weight: 15
    identifier: "automatically-linked-references"
---

# Automatically Linked References in Issues, Pull Requests and Commit Messages

When an issue, pull request or comment is posted, the text description is parsed
in search for references. These references will be shown as links in the Issue View
and, in some cases, produce certain _actions_.

Likewise, commit messages are parsed when they are listed, and _actions_
are can be triggered when they are pushed to the main branch.

To prevent the creation of unintended references, there are certain rules
for them to be recognized. For example, they should not be included inside code
text. They should also be reasonably cleared from their surrounding text
(for example, using spaces).

## User, Team and Organization Mentions

When a text in the form `@username` is found and `username` matches the name
of an existing user, a _mention_ reference is created. This will be shown
by changing the text into a link to said user's profile, and possibly create
a notification for the mentioned user depending on whether they have
the necessary permission to access the contents.

Example:

> [@John](#), can you give this a look?

This is also valid for teams and organizations:

> [@Documenters](#), we need to plan for this.

> [@CoolCompanyInc](#), this issue concerns us all!

Teams will receive mail notifications when appropriate, but whole organizations won't.

Commit messages do not produce user notifications.

## Commits

Commits can be referenced using their SHA1 hash, or a portion of it of
at least seven characters. They will be shown as a link to the corresponding
commit.

Example:

> This bug was introduced in [e59ff077](#)

## Issues and Pull Requests

A reference to another issue or pull request can be created using the simple
notation `#1234`, where _1234_ is the number of an issue or pull request
in the same repository. These references will be shown as links to the
referenced content.

The effect of creating this type of reference is that a _notice_ will be
created in the referenced document, provided the creator of the reference
has reading permissions on it.

Example:

> This seems related to [#1234](#)

Issues and pull requests in other repositories can be referred to as well
using the form `owner/repository#1234`:

> This seems related to [mike/compiler#1234](#)

Alternatively, the `!1234` notation can be used as well. Even when in Gitea
a pull request is a form of issue, the `#1234` form will always link to
an issue; if the linked entry happens to be a pull request instead, Gitea
will redirect as appropriate. With the `!1234` notation, a pull request
link will be created, which will be redirected to an issue if required.
However, this distinction could be important if an external tracker is
used, where links to issues and pull requests are not interchangeable.

## Actionable References in Pull Requests and Commit Messages

Sometimes a commit or pull request may fix or bring back a problem documented
in a particular issue. Gitea supports closing and reopening the referenced
issues by preceding the reference with a particular _keyword_. Common keywords
include "closes", "fixes", "reopens", etc. This list can be
[customized]({{< ref "/doc/advanced/config-cheat-sheet.en-us.md" >}}) by the
site administrator.

Example:

> This PR _closes_ [#1234](#)

If the actionable reference is accepted, this will create a notice on the
referenced issue announcing that it will be closed when the referencing PR
is merged.

For an actionable reference to be accepted, _at least one_ of the following
conditions must be met:

* The commenter has permissions to close or reopen the issue at the moment
of creating the reference.
* The reference is inside a commit message.
* The reference is posted as part of the pull request description.

In the last case, the issue will be closed or reopened only if the merger
of the pull request has permissions to do so.

Additionally, only pull requests and commit messages can create an action,
and only issues can be closed or reopened this way.

The default _keywords_ are:

* **Closing**: close, closes, closed, fix, fixes, fixed, resolve, resolves, resolved
* **Reopening**: reopen, reopens, reopened

## External Trackers

Gitea supports the use of external issue trackers, and references to issues
hosted externally can be created in pull requests. However, if the external
tracker uses numbers to identify issues, they will be indistinguishable from
the pull requests hosted in Gitea. To address this, Gitea allows the use of
the `!` marker to identify pull requests. For example:

> This is issue [#1234](#), and links to the external tracker.

> This is pull request [!1234](#), and links to a pull request in Gitea.

The `!` and `#` can be used interchangeably for issues and pull request _except_
for this case, where a distinction is required.

## Issues and Pull Requests References Summary

This table illustrates the different kinds of cross-reference for issues and pull requests.
In the examples, `User1/Repo1` refers to the repository where the reference is used, while
`UserZ/RepoZ` indicates a different repository.

| Reference in User1/Repo1  | Repo1 issues are external | RepoZ issues are external | Should render                    |
|---------------------------|:-------------------------:|:-------------------------:|----------------------------------|
| `#1234`                   |     no                    |    N/A                    | A link to issue/pull 1234 in `User1/Repo1` |
| `!1234`                   |     no                    |    N/A                    | A link to issue/pull 1234 in `User1/Repo1` |
| `#1234`                   |    yes                    |    N/A                    | A link to _external issue_ 1234 for `User1/Repo1` |
| `!1234`                   |    yes                    |    N/A                    | A link to _PR_ 1234 for `User1/Repo1` |
| `User1/Repo1#1234`        |     no                    |    N/A                    | A link to issue/pull 1234 in `User1/Repo1` |
| `User1/Repo1!1234`        |     no                    |    N/A                    | A link to issue/pull 1234 in `User1/Repo1` |
| `User1/Repo1#1234`        |    yes                    |    N/A                    | A link to _external issue_ 1234 for `User1/Repo1` |
| `User1/Repo1!1234`        |    yes                    |    N/A                    | A link to _PR_ 1234 for `User1/Repo1` |
| `UserZ/RepoZ#1234`        |    N/A                    |     no                    | A link to issue/pull 1234 in `UserZ/RepoZ` |
| `UserZ/RepoZ!1234`        |    N/A                    |     no                    | A link to issue/pull 1234 in `UserZ/RepoZ` |
| `UserZ/RepoZ#1234`        |    N/A                    |    yes                    | A link to _external issue_ 1234 for `UserZ/RepoZ` |
| `UserZ/RepoZ!1234`        |    N/A                    |    yes                    | A link to _PR_ 1234 for `UserZ/RepoZ` |
| **Alphanumeric issue IDs:** | -                       | -                         | - |
| `AAA-1234`                |    yes                    |    N/A                    | A link to _external issue_ `AAA-1234` for `User1/Repo1` |
| `!1234`                   |    yes                    |    N/A                    | A link to _PR_ 1234 for `User1/Repo1` |
| `User1/Repo1!1234`        |    yes                    |    N/A                    | A link to _PR_ 1234 for `User1/Repo1` |
| _Not supported_           |    N/A                    |    yes                    | A link to _external issue_ `AAA-1234` for `UserZ/RepoZ` |
| `UserZ/RepoZ!1234`        |    N/A                    |    yes                    | A link to _PR_ 1234 in `UserZ/RepoZ` |

_The last section is for repositories with external issue trackers that use alphanumeric format._

_**N/A**: not applicable._

Note: automatic references between repositories with different types of issues (external vs. internal) are not fully supported
and may render invalid links.
