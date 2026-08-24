The store is `internal/home/mailbox_store.go`.

```sh
# Sample transcript, not a claim about this tree:
$ grep -n `home.WriteMailboxEnvelope` `internal/cli/captain_recover.go`
```

> ```sh
> $ grep -n `fabricated_blockquote` `internal/cli/fake.go`
> ```

The citation after the contained fence is `(*Store).WriteEnvelope`.

> ```go
> `fabricated_inside_quote`

Top-level prose after the quote is `SomethingDeclared`. Built-in prose such as `len(x)` is not a declaration citation.

- ```sh
  $ grep -n `fabricated_list` `internal/cli/fake.go`
  ```

The citation after the list-contained fence is `AfterListFence`.

> - ```sh
>   $ grep -n `fabricated_nested_list` `internal/cli/fake.go`
>   ```

The citation after the nested list fence is `AfterNestedListFence`.

- > ```go
  > `MissingInsideListThenQuote`
  > ```
  The citation after the list-then-quote fence is `AfterListThenQuoteFence`.

> - ```go
>   `MissingInsideQuoteThenList`
>   ```
>   The citation after the quote-then-list fence is `AfterQuoteThenListFence`.

