---
date: "2019-04-05T16:00:00+02:00"
title: "FAQ"
slug: "faq"
weight: 5
toc: true
draft: false
menu:
  sidebar:
    parent: "help"
    name: "FAQ"
    weight: 5
    identifier: "faq"
---

# Frequently Asked Questions

This page contains some common questions and answers.  
Also see [Support Options]({{< relref "doc/help/seek-help.en-us.md" >}})

* [Difference between 1.x and 1.x.x downloads](#difference-between-1-x-and-1-x-x-downloads)
* [How to migrate from Gogs/GitHub/etc. to Gitea](#how-to-migrate-from-gogs-github-etc-to-gitea)
* [Where does Gitea store "x" file](#where-does-gitea-store-x-file)
* [Not seeing a clone URL or the clone URL being incorrect](#not-seeing-a-clone-url-or-the-clone-url-being-incorrect)
* [Custom Templates not loading or working incorrectly](#custom-templates-not-loading-or-working-incorrectly)
* [Active user vs login prohibited user](#active-user-vs-login-prohibited-user)
* [Setting up logging](#setting-up-logging)
* [What is Swagger?](#what-is-swagger)
* [Adjusting your server for public/private use](#adjusting-your-server-for-public-private-use)
  * [Preventing spammers](#preventing-spammers)
  * [Only allow certain email domains](#only-allow-certain-email-domains)
  * [Only allow/block certain OpenID providers](#only-allow-block-certain-openid-providers)
  * [Issue only users](#issue-only-users)
  * [Enable Fail2ban](#enable-fail2ban)
* [Adding custom themes](#how-to-add-use-custom-themes)
* [SSHD vs built-in SSH](#sshd-vs-built-in-ssh)
* [Gitea is running slow](#gitea-is-running-slow)
* [Can't create repositories/files](#cant-create-repositories-files)
* [Translation is incorrect/how to add more translations](#translation-is-incorrect-how-to-add-more-translations)
* [Hooks aren't running](#hooks-aren-t-running)
* [SSH Issues](#ssh-issues)
  * [SSH Common Errors](#ssh-common-errors)
* [Missing releases after migration repository with tags](#missing-releases-after-migrating-repository-with-tags)
* [LFS Issues](#lfs-issues)
* [How can I create users before starting Gitea](#how-can-i-create-users-before-starting-gitea)
* [How can I enable password reset](#how-can-i-enable-password-reset)
* [How can a user's password be changed](#how-can-a-user-s-password-be-changed)


## Difference between 1.x and 1.x.x downloads
Version 1.7.x will be used for this example.  
**NOTE:** this example applies to Docker images as well!  

On our [downloads page](https://dl.gitea.io/gitea/) you will see a 1.7 directory, as well as directories for 1.7.0, 1.7.1, 1.7.2, 1.7.3, 1.7.4, 1.7.5, and 1.7.6.  
The 1.7 and 1.7.0 directories are **not** the same. The 1.7 directory is built on each merged commit to the [`release/v1.7`](https://github.com/go-gitea/gitea/tree/release/v1.7) branch.  
The 1.7.0 directory, however, is a build that was created when the [`v1.7.0`](https://github.com/go-gitea/gitea/releases/tag/v1.7.0) tag was created.  

This means that 1.x downloads will change as commits are merged to their respective branch (think of it as a separate "master" branch for each release).  
On the other hand, 1.x.x downloads should never change.

## How to migrate from Gogs/GitHub/etc. to Gitea
To migrate from Gogs to Gitea:

* [Gogs version 0.9.146 or less]({{< relref "doc/upgrade/from-gogs.en-us.md" >}})
* [Gogs version 0.11.46.0418](https://github.com/go-gitea/gitea/issues/4286)

To migrate from GitHub to Gitea, you can use Gitea's [Migrator tool](https://gitea.com/gitea/migrator)

To migrate from Gitlab to Gitea, you can use this non-affiliated tool:  
https://github.com/loganinak/MigrateGitlabToGogs

## Where does Gitea store "x" file
* WorkPath
  * Environment variable `GITEA_WORK_DIR`
  * Else binary location
* AppDataPath (default for database, indexers, etc.)
  * `APP_DATA_PATH` from `app.ini`
  * Else `%(WorkPath)/data`
* CustomPath (custom templates)
  * Environment variable `GITEA_CUSTOM`
  * Else `%(WorkPath)/custom`
* HomeDir
  * Unix: Environment variable `HOME`
  * Windows: Environment variable `USERPROFILE`, else environment variables `HOMEDRIVE`+`HOMEPATH`
* RepoRootPath
  * `ROOT` in `app.ini`
  * Else `%(HomeDir)/gitea-repositories`
* INI (config file)
  * `-c` flag
  * Else `%(CustomPath)/conf/app.ini`
* SQLite Database 
  * `PATH` in `database` section of `app.ini`
  * Else `%(AppDataPath)/gitea.db`

## Not seeing a clone URL or the clone URL being incorrect
There are a few places that could make this show incorrectly.

1. If using a reverse proxy, make sure you have followed the correction directions in the [reverse proxy guide]({{< relref "doc/usage/reverse-proxies.en-us.md" >}})
2. Make sure you have correctly set `ROOT_URL` in the `server` section of your `app.ini`

If certain clone options aren't showing up (HTTP/S or SSH), the following options can be checked in your `app.ini`

`DISABLE_HTTP_GIT`: if set to true, there will be no HTTP/HTTPS link  
`DISABLE_SSH`: if set to true, there will be no SSH link  
`SSH_EXPOSE_ANONYMOUS`: if set to false, SSH links will be hidden for anonymous users  


## Custom Templates not loading or working incorrectly
Gitea's custom templates must be added to the correct location or Gitea will not find and use them.  
The correct path for the template(s) will be relative to the `CustomPath`

1. To find `CustomPath`, look for Custom File Root Path in Site Administration -> Configuration 
  * If that doesn't exist, you can try `echo $GITEA_CUSTOM`
2. If you are still unable to find a path, the default can be [calculated above](#where-does-gitea-store-x-file)
3. Once you have figured out the correct custom path, you can refer to the [customizing Gitea]({{< relref "doc/advanced/customizing-gitea.en-us.md" >}}) page to add your template to the correct location.

## Active user vs login prohibited user
In Gitea, an "active" user refers to a user that has activated their account via email.  
A "login prohibited" user is a user that is not allowed to log in to Gitea anymore

## Setting up logging 
* [Official Docs]({{< relref "doc/advanced/logging-documentation.en-us.md" >}})

## What is Swagger?
[Swagger](https://swagger.io/) is what Gitea uses for its API.  
All Gitea instances have the built-in API, though it can be disabled by setting `ENABLE_SWAGGER` to `false` in the `api` section of your `app.ini`  
For more information, refer to Gitea's [API docs]({{< relref "doc/advanced/api-usage.en-us.md" >}})

[Swagger Example](https://try.gitea.io/api/swagger)

## Adjusting your server for public/private use

### Preventing spammers
There are multiple things you can combine to prevent spammers.  

1. By only whitelisting certain domains with OpenID (see below)
2. Setting `ENABLE_CAPTCHA` to `true` in your `app.ini` and properly configuring `RECAPTCHA_SECRET` and `RECAPTCHA_SITEKEY`
3. Settings `DISABLE_REGISTRATION` to `true` and creating new users via the [CLI]({{< relref "doc/usage/command-line.en-us.md" >}}), [API]({{< relref "doc/advanced/api-usage.en-us.md" >}}), or Gitea's Admin UI  

### Only allow certain email domains
You can configure `EMAIL_DOMAIN_WHITELIST` in your app.ini under `[service]`

### Only allow/block certain OpenID providers
You can configure `WHITELISTED_URIS` or `BLACKLISTED_URIS` under `[openid]` in your `app.ini`  
**NOTE:** whitelisted takes precedence, so if it is non-blank then blacklisted is ignored

### Issue only users
The current way to achieve this is to create/modify a user with a max repo creation limit of 0.

### Enable Fail2ban

Use [Fail2Ban]({{ relref "doc/usage/fail2ban-setup.md" >}}) to monitor and stop automated login attempts or other malicious behavior based on log patterns

## How to add/use custom themes
Gitea supports two official themes right now, `gitea` and `arc-green` (`light` and `dark` respectively)  
To add your own theme, currently the only way is to provide a complete theme (not just color overrides)  
  
As an example, let's say our theme is `arc-blue` (this is a real theme, and can be found [in this issue](https://github.com/go-gitea/gitea/issues/6011))  
Name the `.css` file `theme-arc-blue.css` and add it to your custom folder in `custom/pulic/css`  
Allow users to use it by adding `arc-blue` to the list of `THEMES` in your `app.ini`

## SSHD vs built-in SSH
SSHD is the built-in SSH server on most Unix systems.  
Gitea also provides its own SSH server, for usage when SSHD is not available.

## Gitea is running slow
The most common culprit for this is loading federated avatars.  
This can be turned off by setting `ENABLE_FEDERATED_AVATAR` to `false` in your `app.ini`  
Another option that may need to be changed is setting `DISABLE_GRAVATAR` to `true` in your `app.ini`

## Can't create repositories/files
Make sure that Gitea has sufficient permissions to write to its home directory and data directory.  
See [AppDataPath and RepoRootPath](#where-does-gitea-store-x-file)

**Note for Arch users:** At the time of writing this, there is an issue with the Arch package's systemd file including this line:
`ReadWritePaths=/etc/gitea/app.ini`  
Which makes all other paths non-writeable to Gitea.

## Translation is incorrect/how to add more translations
Our translations are currently crowd-sourced on our [Crowdin project](https://crowdin.com/project/gitea)  
Whether you want to change a translation or add a new one, it will need to be there as all translations are overwritten in our CI via the Crowdin integration.

## Hooks aren't running
If Gitea is not running hooks, a common cause is incorrect setup of SSH keys.  
See [SSH Issues](#ssh-issues) for more information.  
  
You can also try logging into the administration panel and running the `Resynchronize pre-receive, update and post-receive hooks of all repositories.` option.

## SSH issues
If you cannot reach repositories over `ssh`, but `https` works fine, consider looking into the following.

First, make sure you can access Gitea via SSH.  
`ssh git@myremote.example`  

If the connection is successful, you should receive an error message like the following:
```
Hi there, You've successfully authenticated, but Gitea does not provide shell access.
If this is unexpected, please log in with password and setup Gitea under another user.
```

If you do not get the above message but still connect, it means your SSH key is **not** being managed by Gitea. This means hooks won't run, among other potential problems.

If you cannot connect at all, your SSH key may not be configured correctly locally. 
This is specific to SSH and not Gitea, so will not be covered here. 

### SSH Common Errors

```
Permission denied (publickey).
fatal: Could not read from remote repository.
```

This error signifies that the server rejected a log in attempt, check the
following things:

* On the client:
  * Ensure the public and private ssh keys are added to the correct Gitea user.
  * Make sure there are no issues in the remote url. In particular, ensure the name of the
    git user (before the `@`) is spelled correctly.
  * Ensure public and private ssh keys are correct on client machine.
* On the server:
  * Make sure the repository exists and is correctly named.
  * Check the permissions of the `.ssh` directory in the system user's home directory.
  * Verify that the correct public keys are added to `.ssh/authorized_keys`.  
    Try to run `Rewrite '.ssh/authorized_keys' file (for Gitea SSH keys)` on the
    Gitea admin panel.
  * Read Gitea logs.
  * Read /var/log/auth (or similar).
  * Check permissions of repositories.

The following is an example of a missing public SSH key where authentication
succeeded, but some other setting is preventing SSH from reaching the correct
repository.

```
fatal: Could not read from remote repository.

Please make sure you have the correct access rights
and the repository exists.
```

In this case, look into the following settings:

* On the server:
  * Make sure that the `git` system user has a usable shell set
    * Verify this with `getent passwd git | cut -d: -f7`
    * `usermod` or `chsh` can be used to modify this.
  * Ensure that the `gitea serv` command in `.ssh/authorized_keys` uses the
    correct configuration file.

## Missing releases after migrating repository with tags

To migrate an repository *with* all tags, you need to do two things:

* Push tags to the repository:
```
 git push --tags
 ```
 
 * (Re-)sync tags of all repositories within Gitea:
 ```
 gitea admin repo-sync-releases
 ```

## LFS Issues

For issues concerning LFS data upload

```
batch response: Authentication required: Authorization error: <GITEA_LFS_URL>/info/lfs/objects/batch                                                                                                              
Check that you have proper access to the repository
error: failed to push some refs to '<GIT_REPO_URL>'
```
Check the value of `LFS_HTTP_AUTH_EXPIRY` in your `app.ini` file.  
By default, your LFS token will expire after 20 minutes. If you have a slow connection or a large file (or both), it may not finish uploading within the time limit. 

You may want to set this value to `60m` or `120m`.

## How can I create users before starting Gitea
Gitea provides a sub-command `gitea migrate` to initialize the database, after which you can use the [admin CLI commands]({{< relref "doc/usage/command-line.en-us.md#admin" >}}) to add users like normal.

## How can I enable password reset
There is no setting for password resets. It is enabled when a [mail service]({{< relref "doc/usage/email-setup.en-us.md" >}}) is configured, and disabled otherwise.

## How can a user's password be changed
- As an **admin**, you can change any user's password (and optionally force them to change it on next login)...
  - By navigating to your `Site Administration -> User Accounts` page and editing a user.  
  - By using the [admin CLI commands]({{< relref "doc/usage/command-line.en-us.md#admin" >}}).  
  Keep in mind most commands will also need a [global flag]({{< relref "doc/usage/command-line.en-us.md#global-options" >}}) to point the CLI at the correct configuration.
- As a **user** you can change it... 
  - In your account `Settings -> Account` page (this method **requires** you to know your current password).
  - By using the `Forgot Password` link.  
   If the `Forgot Password/Account Recovery` page is disabled, please contact your administrator to configure a [mail service]({{< relref "doc/usage/email-setup.en-us.md" >}}).
